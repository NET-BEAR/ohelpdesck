package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/outbox"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrConversationNotFound          = errors.New("conversation_not_found")
	ErrInvalidConversationTransition = errors.New("invalid_conversation_transition")
	ErrVersionConflict               = errors.New("version_conflict")
	ErrInvalidSnoozeDeadline         = errors.New("invalid_snooze_deadline")
	ErrInvalidConversationPriority   = errors.New("invalid_conversation_priority")
)

type ConversationStatus string

const (
	ConversationOpen     ConversationStatus = "open"
	ConversationPending  ConversationStatus = "pending"
	ConversationResolved ConversationStatus = "resolved"
	ConversationSnoozed  ConversationStatus = "snoozed"
)

type ConversationPriority string

const (
	ConversationLow    ConversationPriority = "low"
	ConversationNormal ConversationPriority = "normal"
	ConversationHigh   ConversationPriority = "high"
	ConversationUrgent ConversationPriority = "urgent"
)

type Conversation struct {
	ID, ChannelID            uuid.UUID
	Status                   ConversationStatus
	Priority                 ConversationPriority
	ResolvedAt, SnoozedUntil *time.Time
	Version                  int64
}

type ChangeConversationStatus struct {
	ConversationID  uuid.UUID
	ExpectedVersion int64
	Status          ConversationStatus
	SnoozedUntil    *time.Time
	CorrelationID   uuid.UUID
}

type SetConversationPriority struct {
	ConversationID  uuid.UUID
	ExpectedVersion int64
	Priority        ConversationPriority
	CorrelationID   uuid.UUID
}

// ConversationService owns explicit operator mutations. Authorization and HTTP
// are boundary concerns and intentionally remain outside this provider-neutral service.
type ConversationService struct {
	db     *database.Pool
	outbox outbox.Appender
}

func NewConversationService(db *database.Pool, writers ...outbox.Appender) *ConversationService {
	writer := outbox.Appender(outbox.Writer{})
	if len(writers) == 1 && writers[0] != nil {
		writer = writers[0]
	}
	return &ConversationService{db: db, outbox: writer}
}

func (s *ConversationService) ChangeStatus(ctx context.Context, command ChangeConversationStatus) (Conversation, error) {
	if s == nil || s.db == nil || command.ConversationID == uuid.Nil {
		return Conversation{}, ErrConversationNotFound
	}
	var result Conversation
	err := s.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadConversationForUpdate(ctx, tx, command.ConversationID)
		if err != nil {
			return err
		}
		if current.Version != command.ExpectedVersion {
			return ErrVersionConflict
		}
		if !validStatusTransition(current.Status, command.Status) {
			return ErrInvalidConversationTransition
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return fmt.Errorf("conversation mutation time: %w", err)
		}
		if command.Status == ConversationSnoozed && (command.SnoozedUntil == nil || !command.SnoozedUntil.After(mutationTime)) {
			return ErrInvalidSnoozeDeadline
		}
		var resolvedAt, snoozedUntil *time.Time
		if command.Status == ConversationResolved {
			resolvedAt = &mutationTime
		}
		if command.Status == ConversationSnoozed {
			deadline := command.SnoozedUntil.UTC()
			snoozedUntil = &deadline
		}
		result = Conversation{ID: current.ID, ChannelID: current.ChannelID, Status: command.Status, Priority: current.Priority, ResolvedAt: resolvedAt, SnoozedUntil: snoozedUntil, Version: current.Version + 1}
		if _, err := tx.Exec(ctx, `UPDATE conversations SET status=$2,resolved_at=$3,snoozed_until=$4,version=$5,updated_at=$6 WHERE id=$1`, result.ID, result.Status, result.ResolvedAt, result.SnoozedUntil, result.Version, mutationTime); err != nil {
			return fmt.Errorf("conversation status update: %w", err)
		}
		return s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), AggregateID: result.ID, AggregateType: "conversation", Type: statusEventType(current.Status, result.Status), CorrelationID: correlationID(command.CorrelationID), OccurredAt: mutationTime, Payload: map[string]any{"conversation_id": result.ID, "channel_id": result.ChannelID, "previous_status": current.Status, "status": result.Status, "version": result.Version, "occurred_at": mutationTime}})
	})
	return result, err
}

func (s *ConversationService) SetPriority(ctx context.Context, command SetConversationPriority) (Conversation, error) {
	if s == nil || s.db == nil || command.ConversationID == uuid.Nil {
		return Conversation{}, ErrConversationNotFound
	}
	if !validConversationPriority(command.Priority) {
		return Conversation{}, ErrInvalidConversationPriority
	}
	var result Conversation
	err := s.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		current, err := loadConversationForUpdate(ctx, tx, command.ConversationID)
		if err != nil {
			return err
		}
		if current.Version != command.ExpectedVersion {
			return ErrVersionConflict
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return fmt.Errorf("conversation mutation time: %w", err)
		}
		result = current
		result.Priority = command.Priority
		result.Version++
		if _, err := tx.Exec(ctx, `UPDATE conversations SET priority=$2,version=$3,updated_at=$4 WHERE id=$1`, result.ID, result.Priority, result.Version, mutationTime); err != nil {
			return fmt.Errorf("conversation priority update: %w", err)
		}
		return s.outbox.Append(ctx, tx, outbox.Event{ID: uuid.New(), AggregateID: result.ID, AggregateType: "conversation", Type: "conversation.priority_changed", CorrelationID: correlationID(command.CorrelationID), OccurredAt: mutationTime, Payload: map[string]any{"conversation_id": result.ID, "channel_id": result.ChannelID, "previous_priority": current.Priority, "priority": result.Priority, "version": result.Version, "occurred_at": mutationTime}})
	})
	return result, err
}

func loadConversationForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Conversation, error) {
	var conversation Conversation
	if err := tx.QueryRow(ctx, `SELECT id,channel_id,status,priority,resolved_at,snoozed_until,version FROM conversations WHERE id=$1 FOR UPDATE`, id).Scan(&conversation.ID, &conversation.ChannelID, &conversation.Status, &conversation.Priority, &conversation.ResolvedAt, &conversation.SnoozedUntil, &conversation.Version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrConversationNotFound
		}
		return Conversation{}, fmt.Errorf("conversation lookup: %w", err)
	}
	return conversation, nil
}

func statusEventType(from, to ConversationStatus) string {
	switch to {
	case ConversationPending:
		return "conversation.pending"
	case ConversationSnoozed:
		return "conversation.snoozed"
	case ConversationResolved:
		return "conversation.resolved"
	case ConversationOpen:
		if from == ConversationResolved || from == ConversationSnoozed {
			return "conversation.reopened"
		}
		// SPEC-020 lists conversation.opened as the documented event for entering
		// open. Reopened is reserved for leaving resolved or snoozed.
		return "conversation.opened"
	default:
		return ""
	}
}

func validStatusTransition(from, to ConversationStatus) bool {
	switch from {
	case ConversationOpen:
		return to == ConversationPending || to == ConversationResolved || to == ConversationSnoozed
	case ConversationPending:
		return to == ConversationOpen || to == ConversationResolved || to == ConversationSnoozed
	case ConversationSnoozed:
		return to == ConversationOpen || to == ConversationResolved
	case ConversationResolved:
		return to == ConversationOpen
	default:
		return false
	}
}

func validConversationPriority(priority ConversationPriority) bool {
	return priority == ConversationLow || priority == ConversationNormal || priority == ConversationHigh || priority == ConversationUrgent
}

func correlationID(value uuid.UUID) uuid.UUID {
	if value == uuid.Nil {
		return uuid.New()
	}
	return value
}
