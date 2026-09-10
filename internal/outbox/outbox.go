// Package outbox persists provider-neutral domain facts in the same PostgreSQL
// transaction as the aggregate mutation. Dispatching is deliberately separate.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	ID, AggregateID, CorrelationID uuid.UUID
	CausationID                    *uuid.UUID
	AggregateType, Type            string
	Payload                        any
	OccurredAt                     time.Time
}

type Writer struct{}

// Appender is the transaction-bound outbox boundary used by core application
// services. Implementations must write through the supplied transaction.
type Appender interface {
	Append(context.Context, pgx.Tx, ...Event) error
}

func (Writer) Append(ctx context.Context, tx pgx.Tx, events ...Event) error {
	for _, event := range events {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return fmt.Errorf("outbox payload: %w", err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,payload_version,correlation_id,causation_id,occurred_at) VALUES($1,$2,$3,$4,$5,1,$6,$7,$8)`, event.ID, event.AggregateType, event.AggregateID, event.Type, payload, event.CorrelationID, event.CausationID, event.OccurredAt); err != nil {
			return fmt.Errorf("outbox append: %w", err)
		}
	}
	return nil
}
