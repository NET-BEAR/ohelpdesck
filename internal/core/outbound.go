package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	ErrMessageNotFound          = errors.New("message_not_found")
	ErrInvalidMessageTransition = errors.New("invalid_message_transition")
	ErrIdempotencyConflict      = errors.New("idempotency_conflict")
	ErrInvalidOutbound          = errors.New("invalid_outbound")
)

type MessageStatus string

const (
	MessageQueued MessageStatus = "queued"
	MessageSent   MessageStatus = "sent"
	MessageFailed MessageStatus = "failed"
)

type Message struct {
	ID, ConversationID, ChannelID uuid.UUID
	Status                        MessageStatus
	SentAt                        *time.Time
	Duplicate                     bool
}

type QueueOutboundCommand struct {
	ConversationID   uuid.UUID
	ActorID          uuid.UUID
	Text, HTML       string
	ReplyToMessageID *uuid.UUID
	IdempotencyKey   string
}

type MarkMessageSentCommand struct {
	MessageID         uuid.UUID
	ExternalMessageID string
}

type MarkMessageFailedCommand struct {
	MessageID               uuid.UUID
	ErrorCode, ErrorMessage string
}

// OutboundService owns durable provider-neutral intent and status facts. It
// never calls an external provider; a later worker may invoke these lifecycle
// methods after provider acceptance or definitive correction.
type OutboundService struct {
	db     *database.Pool
	outbox outbox.Appender
}

func NewOutboundService(db *database.Pool, writers ...outbox.Appender) *OutboundService {
	writer := outbox.Appender(outbox.Writer{})
	if len(writers) == 1 && writers[0] != nil {
		writer = writers[0]
	}
	return &OutboundService{db: db, outbox: writer}
}

func (s *OutboundService) QueueOutbound(ctx context.Context, command QueueOutboundCommand) (Message, error) {
	if s == nil || s.db == nil || command.ConversationID == uuid.Nil || command.ActorID == uuid.Nil || blank(command.IdempotencyKey) || (blank(command.Text) && blank(command.HTML)) {
		return Message{}, ErrInvalidOutbound
	}
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	hash, err := outboundRequestHash(command)
	if err != nil {
		return Message{}, fmt.Errorf("outbound request hash: %w", err)
	}
	var result Message
	err = s.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		conversation, err := loadConversationForUpdate(ctx, tx, command.ConversationID, command.ActorID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, command.ActorID.String()+"/outbound/"+command.IdempotencyKey); err != nil {
			return fmt.Errorf("outbound idempotency lock: %w", err)
		}
		var existingHash string
		var existingID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT request_hash,message_id FROM message_idempotency_keys WHERE user_id=$1 AND idempotency_key=$2 FOR UPDATE`, command.ActorID, command.IdempotencyKey).Scan(&existingHash, &existingID)
		if err == nil {
			if existingHash != hash {
				return ErrIdempotencyConflict
			}
			if err := tx.QueryRow(ctx, `SELECT id,conversation_id,channel_id,status,sent_at FROM messages WHERE id=$1`, existingID).Scan(&result.ID, &result.ConversationID, &result.ChannelID, &result.Status, &result.SentAt); err != nil {
				return fmt.Errorf("canonical outbound lookup: %w", err)
			}
			result.Duplicate = true
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("outbound idempotency lookup: %w", err)
		}
		if command.ReplyToMessageID != nil {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1 AND conversation_id=$2`, *command.ReplyToMessageID, conversation.ID).Scan(&count); err != nil {
				return fmt.Errorf("reply target lookup: %w", err)
			}
			if count != 1 {
				return ErrInvalidOutbound
			}
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return fmt.Errorf("outbound mutation time: %w", err)
		}
		result = Message{ID: uuid.New(), ConversationID: conversation.ID, ChannelID: conversation.ChannelID, Status: MessageQueued}
		contentType := ContentText
		if !blank(command.Text) && !blank(command.HTML) {
			contentType = ContentMixed
		} else if blank(command.Text) {
			contentType = ContentHTML
		}
		if _, err := tx.Exec(ctx, `INSERT INTO messages(id,conversation_id,channel_id,direction,actor_type,actor_user_id,content_type,text_content,html_content,reply_to_message_id,status,queued_at) VALUES($1,$2,$3,'outgoing','agent',$4,$5,$6,$7,$8,'queued',$9)`, result.ID, result.ConversationID, result.ChannelID, command.ActorID, contentType, nullIfBlank(command.Text), nullIfBlank(command.HTML), command.ReplyToMessageID, mutationTime); err != nil {
			return fmt.Errorf("outbound message insert: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO message_idempotency_keys(user_id,idempotency_key,request_hash,message_id) VALUES($1,$2,$3,$4)`, command.ActorID, command.IdempotencyKey, hash, result.ID); err != nil {
			return fmt.Errorf("outbound idempotency insert: %w", err)
		}
		return s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), AggregateID: result.ID, AggregateType: "message", Type: "message.queued", CorrelationID: uuid.New(), OccurredAt: mutationTime, Payload: map[string]any{"message_id": result.ID, "conversation_id": result.ConversationID, "channel_id": result.ChannelID, "direction": "outgoing", "actor_type": "agent", "status": "queued", "occurred_at": mutationTime}})
	})
	return result, err
}

func (s *OutboundService) MarkSent(ctx context.Context, command MarkMessageSentCommand) error {
	return s.transition(ctx, command.MessageID, MessageSent, strings.TrimSpace(command.ExternalMessageID), "", "")
}
func (s *OutboundService) MarkFailed(ctx context.Context, command MarkMessageFailedCommand) error {
	if blank(command.ErrorCode) {
		return ErrInvalidOutbound
	}
	return s.transition(ctx, command.MessageID, MessageFailed, "", strings.TrimSpace(command.ErrorCode), strings.TrimSpace(command.ErrorMessage))
}

func (s *OutboundService) transition(ctx context.Context, messageID uuid.UUID, to MessageStatus, externalID, errorCode, errorMessage string) error {
	if s == nil || s.db == nil || messageID == uuid.Nil {
		return ErrMessageNotFound
	}
	return s.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var message Message
		var direction, actorType string
		if err := tx.QueryRow(ctx, `SELECT id,conversation_id,channel_id,status,sent_at,direction,actor_type FROM messages WHERE id=$1 FOR UPDATE`, messageID).Scan(&message.ID, &message.ConversationID, &message.ChannelID, &message.Status, &message.SentAt, &direction, &actorType); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrMessageNotFound
			}
			return fmt.Errorf("outbound message lookup: %w", err)
		}
		if direction != "outgoing" || actorType != "agent" || !validMessageTransition(message.Status, to) {
			return ErrInvalidMessageTransition
		}
		var waitingSince *time.Time
		var closedBy *uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT waiting_since,waiting_closed_by_message_id FROM conversations WHERE id=$1 FOR UPDATE`, message.ConversationID).Scan(&waitingSince, &closedBy); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrMessageNotFound
			}
			return fmt.Errorf("outbound conversation lookup: %w", err)
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return fmt.Errorf("outbound mutation time: %w", err)
		}
		if to == MessageSent {
			if _, err := tx.Exec(ctx, `UPDATE messages SET status='sent',external_message_id=COALESCE(NULLIF($2,''),external_message_id),sent_at=$3,error_code=NULL,error_message=NULL,updated_at=$3 WHERE id=$1`, message.ID, externalID, mutationTime); err != nil {
				return fmt.Errorf("mark message sent: %w", err)
			}
			if _, err := tx.Exec(ctx, `UPDATE conversations SET waiting_closed_since=CASE WHEN waiting_since IS NOT NULL THEN waiting_since ELSE waiting_closed_since END,waiting_closed_by_message_id=CASE WHEN waiting_since IS NOT NULL THEN $2 ELSE waiting_closed_by_message_id END,waiting_since=NULL,first_response_at=COALESCE(first_response_at,$3),last_outbound_at=GREATEST(COALESCE(last_outbound_at,$3),$3),last_activity_at=GREATEST(last_activity_at,$3),version=version+1,updated_at=$3 WHERE id=$1`, message.ConversationID, message.ID, mutationTime); err != nil {
				return fmt.Errorf("mark conversation sent: %w", err)
			}
			return s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), AggregateID: message.ID, AggregateType: "message", Type: "message.sent", CorrelationID: uuid.New(), OccurredAt: mutationTime, Payload: map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "channel_id": message.ChannelID, "status": "sent", "occurred_at": mutationTime}})
		}
		waitingRestored := message.Status == MessageSent && waitingSince == nil && closedBy != nil && *closedBy == message.ID
		if _, err := tx.Exec(ctx, `UPDATE messages SET status='failed',error_code=$2,error_message=$3,updated_at=$4 WHERE id=$1`, message.ID, errorCode, nullIfBlank(errorMessage), mutationTime); err != nil {
			return fmt.Errorf("mark message failed: %w", err)
		}
		if waitingRestored {
			if _, err := tx.Exec(ctx, `UPDATE conversations SET waiting_since=waiting_closed_since,waiting_closed_since=NULL,waiting_closed_by_message_id=NULL,version=version+1,updated_at=$2 WHERE id=$1`, message.ConversationID, mutationTime); err != nil {
				return fmt.Errorf("restore waiting episode: %w", err)
			}
		}
		eventType := "message.failed"
		if message.Status == MessageSent {
			eventType = "message.delivery_corrected"
		}
		return s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), AggregateID: message.ID, AggregateType: "message", Type: eventType, CorrelationID: uuid.New(), OccurredAt: mutationTime, Payload: map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "channel_id": message.ChannelID, "status": "failed", "waiting_restored": waitingRestored, "occurred_at": mutationTime}})
	})
}

func validMessageTransition(from, to MessageStatus) bool {
	return (from == MessageQueued && (to == MessageSent || to == MessageFailed)) || (from == MessageSent && to == MessageFailed)
}

func outboundRequestHash(command QueueOutboundCommand) (string, error) {
	replyTo := ""
	if command.ReplyToMessageID != nil {
		replyTo = command.ReplyToMessageID.String()
	}
	payload, err := json.Marshal(struct{ ConversationID, Text, HTML, ReplyTo string }{command.ConversationID.String(), strings.TrimSpace(command.Text), strings.TrimSpace(command.HTML), replyTo})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}
