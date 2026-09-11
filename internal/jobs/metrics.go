package jobs

import (
	"context"
	"fmt"

	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/telemetry"
)

// MetricsReader reads durable-work aggregates from PostgreSQL for the internal
// metrics endpoint. It deliberately exposes no payloads, IDs or handler labels.
type MetricsReader struct{ db *database.Pool }

func NewMetricsReader(db *database.Pool) *MetricsReader { return &MetricsReader{db: db} }

func (r *MetricsReader) QueueMetrics(ctx context.Context) (telemetry.QueueMetricsSnapshot, error) {
	if r == nil || r.db == nil {
		return telemetry.QueueMetricsSnapshot{}, fmt.Errorf("queue metrics reader unavailable")
	}
	result := telemetry.QueueMetricsSnapshot{JobDepth: map[string]float64{}}
	if err := r.db.QueryRow(ctx, `SELECT count(*)::double precision,COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(created_at)),0) FROM outbox_events WHERE dispatched_at IS NULL`).Scan(&result.OutboxUndispatched, &result.OutboxLagSeconds); err != nil {
		return telemetry.QueueMetricsSnapshot{}, fmt.Errorf("read outbox metrics: %w", err)
	}
	rows, err := r.db.Query(ctx, `SELECT status::text,count(*)::double precision FROM jobs GROUP BY status`)
	if err != nil {
		return telemetry.QueueMetricsSnapshot{}, fmt.Errorf("read job metrics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var depth float64
		if err = rows.Scan(&status, &depth); err != nil {
			return telemetry.QueueMetricsSnapshot{}, fmt.Errorf("scan job metrics: %w", err)
		}
		result.JobDepth[status] = depth
	}
	if err = rows.Err(); err != nil {
		return telemetry.QueueMetricsSnapshot{}, fmt.Errorf("iterate job metrics: %w", err)
	}
	return result, nil
}
