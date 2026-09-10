// Package core owns provider-neutral support facts. Provider adapters must pass
// normalized values here and must never leak provider request models into core.
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/outbox"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrChannelDisabled = errors.New("channel disabled")
	ErrInvalidInbound  = errors.New("invalid normalized inbound")
)

type ContentType string

const (
	ContentText  ContentType = "text"
	ContentHTML  ContentType = "html"
	ContentMixed ContentType = "mixed"
)

type ExternalSender struct{ ExternalUserID, ExternalChatID, Username, DisplayName string }
type NormalizedInboundMessage struct {
	ProviderEventID, ExternalMessageID string
	ChannelID                          uuid.UUID
	ExternalThreadID                   string
	Sender                             ExternalSender
	Text, HTML                         string
	ContentType                        ContentType
}
type ReceiveResult struct {
	ContactID, IdentityID, ConversationID, MessageID uuid.UUID
	Duplicate                                        bool
}
type ReceiveInboundService struct {
	db     *database.Pool
	outbox outbox.Appender
}

// NewReceiveInboundService composes the real PostgreSQL writer by default.
// An explicit writer exists only for transaction-bound failure injection in
// tests; callers must never supply a writer that persists outside tx.
func NewReceiveInboundService(db *database.Pool, writers ...outbox.Appender) *ReceiveInboundService {
	writer := outbox.Appender(outbox.Writer{})
	if len(writers) == 1 && writers[0] != nil {
		writer = writers[0]
	}
	return &ReceiveInboundService{db: db, outbox: writer}
}

func (s *ReceiveInboundService) ReceiveInbound(ctx context.Context, input NormalizedInboundMessage) (ReceiveResult, error) {
	if s == nil || s.db == nil || input.ChannelID == uuid.Nil || blank(input.ExternalMessageID) || blank(input.ExternalThreadID) || blank(input.Sender.ExternalUserID) || !validContent(input.ContentType) {
		return ReceiveResult{}, ErrInvalidInbound
	}
	result := ReceiveResult{}
	err := s.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var enabled bool
		var status string
		if err := tx.QueryRow(ctx, `SELECT enabled,status FROM channels WHERE id=$1 FOR UPDATE`, input.ChannelID).Scan(&enabled, &status); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrChannelDisabled
			}
			return fmt.Errorf("channel lookup: %w", err)
		}
		if !enabled || status != "active" {
			return ErrChannelDisabled
		}
		// This is the idempotency boundary. It must precede every fact mutation:
		// a provider can replay one external message with inconsistent sender or
		// thread attributes, and the first committed delivery is canonical.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.ChannelID.String()+"/message/"+input.ExternalMessageID); err != nil {
			return fmt.Errorf("message lock: %w", err)
		}
		canonical, found, err := findCanonicalInbound(ctx, tx, input.ChannelID, input.ExternalMessageID)
		if err != nil {
			return err
		}
		if found {
			result = canonical
			return nil
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.ChannelID.String()+"/identity/"+input.Sender.ExternalUserID); err != nil {
			return fmt.Errorf("identity lock: %w", err)
		}
		var contactID, identityID, identityChannelID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT contact_id,id,channel_id FROM contact_identities WHERE channel_id=$1 AND external_user_id=$2 FOR UPDATE`, input.ChannelID, input.Sender.ExternalUserID).Scan(&contactID, &identityID, &identityChannelID)
		if errors.Is(err, pgx.ErrNoRows) {
			contactID, identityID = uuid.New(), uuid.New()
			if _, err = tx.Exec(ctx, `INSERT INTO contacts(id,name) VALUES($1,$2)`, contactID, input.Sender.DisplayName); err != nil {
				return fmt.Errorf("contact insert: %w", err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO contact_identities(id,contact_id,channel_id,external_user_id,external_chat_id,username,display_name) VALUES($1,$2,$3,$4,$5,$6,$7)`, identityID, contactID, input.ChannelID, input.Sender.ExternalUserID, nullIfBlank(input.Sender.ExternalChatID), nullIfBlank(input.Sender.Username), nullIfBlank(input.Sender.DisplayName)); err != nil {
				return fmt.Errorf("identity insert: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("identity lookup: %w", err)
		} else if identityChannelID != input.ChannelID {
			return fmt.Errorf("identity channel mismatch")
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, input.ChannelID.String()+"/conversation/"+input.ExternalThreadID+"/"+identityID.String()); err != nil {
			return fmt.Errorf("conversation lock: %w", err)
		}
		var conversationID, conversationContactID, conversationIdentityID, conversationChannelID uuid.UUID
		var version int64
		var created bool
		err = tx.QueryRow(ctx, `SELECT id,version,contact_id,contact_identity_id,channel_id FROM conversations WHERE channel_id=$1 AND external_thread_id=$2 AND contact_identity_id=$3 AND is_current FOR UPDATE`, input.ChannelID, input.ExternalThreadID, identityID).Scan(&conversationID, &version, &conversationContactID, &conversationIdentityID, &conversationChannelID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("conversation lookup: %w", err)
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return fmt.Errorf("mutation time: %w", err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			conversationID, version, created = uuid.New(), 1, true
			if _, err = tx.Exec(ctx, `INSERT INTO conversations(id,contact_id,contact_identity_id,channel_id,external_thread_id,status,waiting_since,last_inbound_at,last_activity_at,version) VALUES($1,$2,$3,$4,$5,'open',$6,$6,$6,1)`, conversationID, contactID, identityID, input.ChannelID, input.ExternalThreadID, mutationTime); err != nil {
				return fmt.Errorf("conversation insert: %w", err)
			}
		} else if conversationContactID != contactID || conversationIdentityID != identityID || conversationChannelID != input.ChannelID {
			return fmt.Errorf("conversation channel mismatch")
		}
		messageID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO messages(id,conversation_id,channel_id,external_message_id,direction,actor_type,content_type,text_content,html_content,status,received_at) VALUES($1,$2,$3,$4,'incoming','customer',$5,$6,$7,'received',$8)`, messageID, conversationID, input.ChannelID, input.ExternalMessageID, input.ContentType, nullIfBlank(input.Text), nullIfBlank(input.HTML), mutationTime); err != nil {
			return fmt.Errorf("message insert: %w", err)
		}
		if !created {
			if _, err = tx.Exec(ctx, `UPDATE conversations SET status='open', snoozed_until=NULL, resolved_at=NULL, waiting_since=COALESCE(waiting_since,$2), last_inbound_at=$2, last_activity_at=GREATEST(last_activity_at,$2), version=version+1, updated_at=$2 WHERE id=$1`, conversationID, mutationTime); err != nil {
				return fmt.Errorf("conversation update: %w", err)
			}
			version++
		}
		correlationID := uuid.New()
		events := []outbox.Event{{ID: uuid.New(), AggregateID: messageID, AggregateType: "message", Type: "message.received", CorrelationID: correlationID, OccurredAt: mutationTime, Payload: map[string]any{"message_id": messageID, "conversation_id": conversationID, "channel_id": input.ChannelID, "identity_id": identityID, "direction": "incoming", "actor_type": "customer", "status": "received", "occurred_at": mutationTime}}}
		if created {
			events = append(events, outbox.Event{ID: uuid.New(), AggregateID: conversationID, AggregateType: "conversation", Type: "conversation.opened", CorrelationID: correlationID, OccurredAt: mutationTime, Payload: map[string]any{"conversation_id": conversationID, "channel_id": input.ChannelID, "reason": "inbound", "version": version, "occurred_at": mutationTime}})
		}
		if err = s.outbox.Append(ctx, tx, events...); err != nil {
			return err
		}
		result = ReceiveResult{ContactID: contactID, IdentityID: identityID, ConversationID: conversationID, MessageID: messageID}
		return nil
	})
	return result, err
}

// findCanonicalInbound reads all IDs from the already-persisted message path.
// It is intentionally called before identity/conversation resolution so retries
// cannot create facts from altered provider attributes.
func findCanonicalInbound(ctx context.Context, tx pgx.Tx, channelID uuid.UUID, externalMessageID string) (ReceiveResult, bool, error) {
	var result ReceiveResult
	var messageChannelID, conversationChannelID, identityChannelID, conversationContactID uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT ci.contact_id, ci.id, c.id, m.id,
		       m.channel_id, c.channel_id, ci.channel_id, c.contact_id
		FROM messages m
		JOIN conversations c ON c.id=m.conversation_id
		JOIN contact_identities ci ON ci.id=c.contact_identity_id
		WHERE m.channel_id=$1 AND m.external_message_id=$2`, channelID, externalMessageID).
		Scan(&result.ContactID, &result.IdentityID, &result.ConversationID, &result.MessageID,
			&messageChannelID, &conversationChannelID, &identityChannelID, &conversationContactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReceiveResult{}, false, nil
	}
	if err != nil {
		return ReceiveResult{}, false, fmt.Errorf("canonical message lookup: %w", err)
	}
	if messageChannelID != channelID || conversationChannelID != channelID || identityChannelID != channelID || conversationContactID != result.ContactID {
		return ReceiveResult{}, false, fmt.Errorf("canonical message channel mismatch")
	}
	result.Duplicate = true
	return result, true, nil
}

func blank(v string) bool { return strings.TrimSpace(v) == "" }
func nullIfBlank(v string) any {
	if blank(v) {
		return nil
	}
	return strings.TrimSpace(v)
}
func validContent(v ContentType) bool {
	return v == ContentText || v == ContentHTML || v == ContentMixed
}
