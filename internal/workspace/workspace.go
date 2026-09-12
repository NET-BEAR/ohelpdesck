// Package workspace provides provider-neutral operator read models.
package workspace

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrNotFound     = errors.New("conversation_not_found")
	ErrForbidden    = errors.New("conversation_forbidden")
	ErrInvalidQuery = errors.New("invalid_workspace_query")
)

type UserSummary struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}
type ChannelSummary struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Type   string    `json:"type"`
	Status string    `json:"status"`
}
type ContactSummary struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"display_name"`
}
type Capabilities struct {
	CanReply    bool `json:"can_reply"`
	CanReassign bool `json:"can_reassign"`
}
type MessagePreview struct {
	ID        uuid.UUID `json:"id"`
	Direction string    `json:"direction"`
	Status    string    `json:"status"`
	Preview   string    `json:"preview"`
	CreatedAt time.Time `json:"created_at"`
}
type ListItem struct {
	ID             uuid.UUID                 `json:"id"`
	Number         int64                     `json:"number"`
	Channel        ChannelSummary            `json:"channel"`
	Contact        ContactSummary            `json:"contact"`
	Assignee       *UserSummary              `json:"assignee"`
	Status         core.ConversationStatus   `json:"status"`
	Priority       core.ConversationPriority `json:"priority"`
	WaitingSince   *time.Time                `json:"waiting_since"`
	LastActivityAt time.Time                 `json:"last_activity_at"`
	LastMessage    *MessagePreview           `json:"last_message"`
	Version        int64                     `json:"version"`
}
type Detail struct {
	ListItem
	Subject         *string      `json:"subject"`
	FirstResponseAt *time.Time   `json:"first_response_at"`
	LastInboundAt   *time.Time   `json:"last_inbound_at"`
	LastOutboundAt  *time.Time   `json:"last_outbound_at"`
	ResolvedAt      *time.Time   `json:"resolved_at"`
	SnoozedUntil    *time.Time   `json:"snoozed_until"`
	Capabilities    Capabilities `json:"capabilities"`
}
type Page struct {
	Items      []ListItem `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}
type ListQuery struct {
	Limit      int
	Cursor     string
	ChannelIDs []uuid.UUID
	Statuses   []core.ConversationStatus
	Priorities []core.ConversationPriority
	Assignee   *uuid.UUID
	Unassigned bool
}

type Service struct{ db *database.Pool }

func NewService(db *database.Pool) *Service { return &Service{db: db} }

type listCursor struct {
	V      int       `json:"v"`
	Number int64     `json:"number"`
	ID     uuid.UUID `json:"id"`
}

func decodeCursor(raw string) (*listCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 512 {
		return nil, ErrInvalidQuery
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return nil, ErrInvalidQuery
	}
	var c listCursor
	if json.Unmarshal(b, &c) != nil || c.V != 1 || c.Number < 1 || c.ID == uuid.Nil {
		return nil, ErrInvalidQuery
	}
	return &c, nil
}
func encodeCursor(c listCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func validStatus(v core.ConversationStatus) bool {
	return v == core.ConversationOpen || v == core.ConversationPending || v == core.ConversationResolved || v == core.ConversationSnoozed
}
func validPriority(v core.ConversationPriority) bool {
	return v == core.ConversationLow || v == core.ConversationNormal || v == core.ConversationHigh || v == core.ConversationUrgent
}

func (s *Service) List(ctx context.Context, actor uuid.UUID, q ListQuery) (Page, error) {
	if s == nil || s.db == nil || actor == uuid.Nil {
		return Page{}, ErrForbidden
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 {
		return Page{}, ErrInvalidQuery
	}
	c, e := decodeCursor(q.Cursor)
	if e != nil {
		return Page{}, e
	}
	for _, v := range q.Statuses {
		if !validStatus(v) {
			return Page{}, ErrInvalidQuery
		}
	}
	for _, v := range q.Priorities {
		if !validPriority(v) {
			return Page{}, ErrInvalidQuery
		}
	}
	args := []any{actor}
	where := []string{"EXISTS (SELECT 1 FROM channel_memberships cm WHERE cm.channel_id=c.channel_id AND cm.user_id=$1 AND cm.can_read)"}
	add := func(value any) string { args = append(args, value); return fmt.Sprintf("$%d", len(args)) }
	if len(q.ChannelIDs) > 0 {
		where = append(where, "c.channel_id = ANY("+add(q.ChannelIDs)+")")
	}
	if len(q.Statuses) > 0 {
		where = append(where, "c.status = ANY("+add(q.Statuses)+")")
	}
	if len(q.Priorities) > 0 {
		where = append(where, "c.priority = ANY("+add(q.Priorities)+")")
	}
	if q.Assignee != nil {
		where = append(where, "c.assignee_id="+add(*q.Assignee))
	} else if q.Unassigned {
		where = append(where, "c.assignee_id IS NULL")
	}
	if c != nil {
		where = append(where, "(c.number,c.id) < ("+add(c.Number)+","+add(c.ID)+")")
	}
	args = append(args, q.Limit+1)
	sql := `SELECT c.id,c.number,ch.id,ch.name,ch.type,CASE WHEN ch.enabled THEN 'active' ELSE 'disabled' END,ct.id,ct.name,a.id,a.name,c.status,c.priority,c.waiting_since,c.last_activity_at,c.version,lm.id,lm.direction,lm.status,lm.text_content,lm.created_at
 FROM conversations c JOIN channels ch ON ch.id=c.channel_id JOIN contacts ct ON ct.id=c.contact_id LEFT JOIN users a ON a.id=c.assignee_id
 LEFT JOIN LATERAL (SELECT id,direction,status,text_content,created_at FROM messages WHERE conversation_id=c.id ORDER BY created_at DESC,id DESC LIMIT 1) lm ON true
 WHERE ` + strings.Join(where, " AND ") + ` ORDER BY c.number DESC,c.id DESC LIMIT $` + fmt.Sprint(len(args))
	rows, e := s.db.Query(ctx, sql, args...)
	if e != nil {
		return Page{}, fmt.Errorf("workspace list: %w", e)
	}
	defer rows.Close()
	items := make([]ListItem, 0, q.Limit+1)
	for rows.Next() {
		var x ListItem
		var aid *uuid.UUID
		var aname *string
		var mid *uuid.UUID
		var direction, status, body *string
		var created *time.Time
		if e := rows.Scan(&x.ID, &x.Number, &x.Channel.ID, &x.Channel.Name, &x.Channel.Type, &x.Channel.Status, &x.Contact.ID, &x.Contact.DisplayName, &aid, &aname, &x.Status, &x.Priority, &x.WaitingSince, &x.LastActivityAt, &x.Version, &mid, &direction, &status, &body, &created); e != nil {
			return Page{}, fmt.Errorf("workspace list scan: %w", e)
		}
		if aid != nil {
			x.Assignee = &UserSummary{ID: *aid, Name: *aname}
		}
		if mid != nil {
			p := ""
			if body != nil {
				r := []rune(*body)
				if len(r) > 280 {
					r = r[:280]
				}
				p = string(r)
			}
			x.LastMessage = &MessagePreview{ID: *mid, Direction: *direction, Status: *status, Preview: p, CreatedAt: *created}
		}
		items = append(items, x)
	}
	if e := rows.Err(); e != nil {
		return Page{}, fmt.Errorf("workspace list rows: %w", e)
	}
	result := Page{Items: items}
	if len(items) > q.Limit {
		last := items[q.Limit-1]
		result.Items = items[:q.Limit]
		next := encodeCursor(listCursor{V: 1, Number: last.Number, ID: last.ID})
		result.NextCursor = &next
	}
	return result, nil
}

func (s *Service) Detail(ctx context.Context, actor, id uuid.UUID, canReply, canReassign bool) (Detail, error) {
	if s == nil || s.db == nil || actor == uuid.Nil || id == uuid.Nil {
		return Detail{}, ErrNotFound
	}
	var d Detail
	var aid *uuid.UUID
	var aname *string
	err := s.db.QueryRow(ctx, `SELECT c.id,c.number,ch.id,ch.name,ch.type,CASE WHEN ch.enabled THEN 'active' ELSE 'disabled' END,ct.id,ct.name,a.id,a.name,c.status,c.priority,c.waiting_since,c.last_activity_at,c.version,c.subject,c.first_response_at,c.last_inbound_at,c.last_outbound_at,c.resolved_at,c.snoozed_until FROM conversations c JOIN channels ch ON ch.id=c.channel_id JOIN contacts ct ON ct.id=c.contact_id LEFT JOIN users a ON a.id=c.assignee_id WHERE c.id=$1 AND EXISTS (SELECT 1 FROM channel_memberships cm WHERE cm.channel_id=c.channel_id AND cm.user_id=$2 AND cm.can_read)`, id, actor).Scan(&d.ID, &d.Number, &d.Channel.ID, &d.Channel.Name, &d.Channel.Type, &d.Channel.Status, &d.Contact.ID, &d.Contact.DisplayName, &aid, &aname, &d.Status, &d.Priority, &d.WaitingSince, &d.LastActivityAt, &d.Version, &d.Subject, &d.FirstResponseAt, &d.LastInboundAt, &d.LastOutboundAt, &d.ResolvedAt, &d.SnoozedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		e := s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1)", id).Scan(&exists)
		if e != nil {
			return Detail{}, fmt.Errorf("workspace detail lookup: %w", e)
		}
		if exists {
			return Detail{}, ErrForbidden
		}
		return Detail{}, ErrNotFound
	}
	if err != nil {
		return Detail{}, fmt.Errorf("workspace detail: %w", err)
	}
	if aid != nil {
		d.Assignee = &UserSummary{ID: *aid, Name: *aname}
	}
	d.Capabilities = Capabilities{CanReply: canReply, CanReassign: canReassign}
	return d, nil
}

type TimelineMessage struct {
	ID             uuid.UUID `json:"id"`
	ConversationID uuid.UUID `json:"conversation_id"`
	Direction      string    `json:"direction"`
	Actor          struct {
		Type string       `json:"type"`
		User *UserSummary `json:"user"`
	} `json:"actor"`
	Content struct {
		Type string  `json:"type"`
		Text *string `json:"text"`
		HTML *string `json:"html"`
	} `json:"content"`
	Status           string     `json:"status"`
	CreatedAt        time.Time  `json:"created_at"`
	QueuedAt         *time.Time `json:"queued_at"`
	SentAt           *time.Time `json:"sent_at"`
	DeliveredAt      *time.Time `json:"delivered_at"`
	ReplyToMessageID *uuid.UUID `json:"reply_to_message_id"`
	Failed           *struct {
		Code string `json:"code"`
	} `json:"failed"`
}
type MessagePage struct {
	Items          []TimelineMessage `json:"items"`
	PreviousCursor *string           `json:"previous_cursor"`
	NextCursor     *string           `json:"next_cursor"`
}
type messageCursor struct {
	V         int       `json:"v"`
	CreatedAt time.Time `json:"created_at"`
	ID        uuid.UUID `json:"id"`
}

func decodeMessageCursor(raw string) (*messageCursor, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 512 {
		return nil, ErrInvalidQuery
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return nil, ErrInvalidQuery
	}
	var c messageCursor
	if json.Unmarshal(b, &c) != nil || c.V != 1 || c.ID == uuid.Nil || c.CreatedAt.IsZero() {
		return nil, ErrInvalidQuery
	}
	return &c, nil
}
func encodeMessageCursor(c messageCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Service) Messages(ctx context.Context, actor, conversationID uuid.UUID, before, after string, limit int) (MessagePage, error) {
	if s == nil || s.db == nil || actor == uuid.Nil || conversationID == uuid.Nil {
		return MessagePage{}, ErrNotFound
	}
	if before != "" && after != "" {
		return MessagePage{}, ErrInvalidQuery
	}
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return MessagePage{}, ErrInvalidQuery
	}
	bc, e := decodeMessageCursor(before)
	if e != nil {
		return MessagePage{}, e
	}
	ac, e := decodeMessageCursor(after)
	if e != nil {
		return MessagePage{}, e
	}
	var authorized bool
	e = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM conversations c JOIN channel_memberships cm ON cm.channel_id=c.channel_id WHERE c.id=$1 AND cm.user_id=$2 AND cm.can_read)`, conversationID, actor).Scan(&authorized)
	if e != nil {
		return MessagePage{}, fmt.Errorf("workspace message access: %w", e)
	}
	if !authorized {
		var exists bool
		if e = s.db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1)", conversationID).Scan(&exists); e != nil {
			return MessagePage{}, fmt.Errorf("workspace message existence: %w", e)
		}
		if exists {
			return MessagePage{}, ErrForbidden
		}
		return MessagePage{}, ErrNotFound
	}
	args := []any{conversationID}
	where := "conversation_id=$1"
	direction := "ASC"
	if bc != nil {
		args = append(args, bc.CreatedAt, bc.ID)
		where += " AND (created_at,id) < ($2,$3)"
		direction = "DESC"
	}
	if ac != nil {
		args = append(args, ac.CreatedAt, ac.ID)
		where += " AND (created_at,id) > ($2,$3)"
	}
	args = append(args, limit+1)
	rows, e := s.db.Query(ctx, `SELECT m.id,m.conversation_id,m.direction,m.actor_type,u.id,u.name,m.content_type,m.text_content,m.html_content,m.status,m.created_at,m.queued_at,m.sent_at,m.delivered_at,m.reply_to_message_id,m.error_code FROM messages m LEFT JOIN users u ON u.id=m.actor_user_id WHERE `+where+` ORDER BY m.created_at `+direction+`,m.id `+direction+` LIMIT $`+fmt.Sprint(len(args)), args...)
	if e != nil {
		return MessagePage{}, fmt.Errorf("workspace messages: %w", e)
	}
	defer rows.Close()
	items := make([]TimelineMessage, 0, limit+1)
	for rows.Next() {
		var m TimelineMessage
		var uid *uuid.UUID
		var uname *string
		var code *string
		if e = rows.Scan(&m.ID, &m.ConversationID, &m.Direction, &m.Actor.Type, &uid, &uname, &m.Content.Type, &m.Content.Text, &m.Content.HTML, &m.Status, &m.CreatedAt, &m.QueuedAt, &m.SentAt, &m.DeliveredAt, &m.ReplyToMessageID, &code); e != nil {
			return MessagePage{}, fmt.Errorf("workspace messages scan: %w", e)
		}
		if uid != nil {
			m.Actor.User = &UserSummary{ID: *uid, Name: *uname}
		}
		if m.Status == "failed" && code != nil {
			m.Failed = &struct {
				Code string `json:"code"`
			}{Code: *code}
		}
		items = append(items, m)
	}
	if e = rows.Err(); e != nil {
		return MessagePage{}, fmt.Errorf("workspace messages rows: %w", e)
	}
	if bc != nil {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	page := MessagePage{Items: items}
	if len(items) > limit {
		if bc != nil {
			items = items[1:]
			page.Items = items
		} else {
			page.Items = items[:limit]
		}
		if len(page.Items) > 0 {
			first := page.Items[0]
			last := page.Items[len(page.Items)-1]
			p := encodeMessageCursor(messageCursor{V: 1, CreatedAt: first.CreatedAt, ID: first.ID})
			n := encodeMessageCursor(messageCursor{V: 1, CreatedAt: last.CreatedAt, ID: last.ID})
			if bc != nil {
				page.PreviousCursor = &p
			} else {
				page.NextCursor = &n
			}
		}
	}
	return page, nil
}
