// Package jobs turns committed outbox facts into PostgreSQL-backed, at-least-once
// jobs. It deliberately contains no provider client or provider-specific model.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrStaleJobLease  = errors.New("stale job lease")
	ErrUnknownHandler = errors.New("unknown job handler")
)

type Status string

const (
	Pending   Status = "pending"
	Running   Status = "running"
	Completed Status = "completed"
	Dead      Status = "dead"
	Cancelled Status = "cancelled"
)

type FailureClass string

const (
	Transient FailureClass = "transient"
	Permanent FailureClass = "permanent"
)

type JobError struct {
	Class         FailureClass
	Code, Message string
	RetryAfter    *time.Duration
	Cause         error
}

func (e *JobError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

type DomainEvent struct {
	ID            uuid.UUID
	Type          string
	Payload       []byte
	CorrelationID uuid.UUID
	CausationID   *uuid.UUID
	OccurredAt    time.Time
}
type Job struct {
	ID                    uuid.UUID
	Type, Handler         string
	Status                Status
	EventID               *uuid.UUID
	Attempts, MaxAttempts int
	Priority              int
	RunAt                 time.Time
	LockedBy              string
	LeaseToken            uuid.UUID
	LeaseExpiresAt        time.Time
}
type Lease struct {
	Job      Job
	WorkerID string
	Token    uuid.UUID
}
type Route struct{ EventType, Handler string }
type Handler interface {
	Handle(context.Context, pgx.Tx, DomainEvent) error
}

type Registry struct {
	routes   map[string][]Route
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{routes: map[string][]Route{}, handlers: map[string]Handler{}}
}
func (r *Registry) Register(route Route, handler Handler) error {
	route.EventType, route.Handler = strings.TrimSpace(route.EventType), strings.TrimSpace(route.Handler)
	if route.EventType == "" || route.Handler == "" || handler == nil {
		return fmt.Errorf("invalid job route")
	}
	if _, exists := r.handlers[route.Handler]; exists {
		return fmt.Errorf("duplicate job handler")
	}
	r.handlers[route.Handler] = handler
	r.routes[route.EventType] = append(r.routes[route.EventType], route)
	sort.Slice(r.routes[route.EventType], func(i, j int) bool {
		return r.routes[route.EventType][i].Handler < r.routes[route.EventType][j].Handler
	})
	return nil
}
func (r *Registry) EventTypes() []string {
	v := make([]string, 0, len(r.routes))
	for e := range r.routes {
		v = append(v, e)
	}
	sort.Strings(v)
	return v
}
func (r *Registry) Routes(eventType string) []Route {
	return append([]Route(nil), r.routes[eventType]...)
}
func (r *Registry) Resolve(handler string) (Handler, bool) {
	h, ok := r.handlers[handler]
	return h, ok
}

type Dispatcher struct {
	db       *database.Pool
	registry *Registry
}

func NewDispatcher(db *database.Pool, registry *Registry) *Dispatcher {
	return &Dispatcher{db: db, registry: registry}
}

type DispatchResult struct{ Events, Jobs int }

func (d *Dispatcher) DispatchBatch(ctx context.Context, limit int) (result DispatchResult, err error) {
	if limit <= 0 {
		return result, fmt.Errorf("dispatch limit must be positive")
	}
	types := d.registry.EventTypes()
	if len(types) == 0 {
		return result, nil
	}
	err = d.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id,event_type,correlation_id,causation_id FROM outbox_events WHERE dispatched_at IS NULL AND event_type=ANY($1::text[]) ORDER BY created_at,id FOR UPDATE SKIP LOCKED LIMIT $2`, types, limit)
		if e != nil {
			return fmt.Errorf("select undispatched events: %w", e)
		}
		defer rows.Close()
		type event struct {
			id          uuid.UUID
			typ         string
			correlation uuid.UUID
			causation   *uuid.UUID
		}
		var events []event
		for rows.Next() {
			var x event
			if e = rows.Scan(&x.id, &x.typ, &x.correlation, &x.causation); e != nil {
				return e
			}
			events = append(events, x)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		for _, event := range events {
			routes := d.registry.Routes(event.typ)
			if len(routes) == 0 {
				continue
			}
			for _, route := range routes {
				tag, e := tx.Exec(ctx, `INSERT INTO jobs(id,type,handler,payload,event_id,correlation_id,causation_id) VALUES($1,'domain_event',$2,jsonb_build_object('event_id',$3::text),$3,$4,$5) ON CONFLICT (event_id,handler) WHERE event_id IS NOT NULL DO NOTHING`, uuid.New(), route.Handler, event.id, event.correlation, event.causation)
				if e != nil {
					return fmt.Errorf("insert job: %w", e)
				}
				result.Jobs += int(tag.RowsAffected())
			}
			if _, e := tx.Exec(ctx, `UPDATE outbox_events SET dispatched_at=clock_timestamp() WHERE id=$1`, event.id); e != nil {
				return fmt.Errorf("mark dispatched: %w", e)
			}
			result.Events++
		}
		return nil
	})
	return result, err
}

type Repository struct{ db *database.Pool }

func NewRepository(db *database.Pool) *Repository { return &Repository{db: db} }
func (r *Repository) Claim(ctx context.Context, workerID string, lease time.Duration) (*Lease, error) {
	if strings.TrimSpace(workerID) == "" || lease <= 0 {
		return nil, fmt.Errorf("invalid job lease")
	}
	token := uuid.New()
	var job Job
	e := r.db.QueryRow(ctx, `WITH candidate AS (SELECT id FROM jobs WHERE status='pending' AND run_at<=clock_timestamp() ORDER BY priority,run_at,created_at FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE jobs j SET status='running',locked_by=$1,locked_at=clock_timestamp(),lease_expires_at=clock_timestamp()+$2::interval,lease_token=$3,attempts=attempts+1,updated_at=clock_timestamp() FROM candidate WHERE j.id=candidate.id RETURNING j.id,j.type,j.handler,j.status,j.event_id,j.attempts,j.max_attempts,j.priority,j.run_at,j.locked_by,j.lease_token,j.lease_expires_at`, workerID, lease.String(), token).Scan(&job.ID, &job.Type, &job.Handler, &job.Status, &job.EventID, &job.Attempts, &job.MaxAttempts, &job.Priority, &job.RunAt, &job.LockedBy, &job.LeaseToken, &job.LeaseExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, nil
	}
	if e != nil {
		return nil, fmt.Errorf("claim job: %w", e)
	}
	return &Lease{Job: job, WorkerID: workerID, Token: token}, nil
}
func (r *Repository) Extend(ctx context.Context, lease Lease, duration time.Duration) error {
	if duration <= 0 {
		return fmt.Errorf("invalid lease duration")
	}
	tag, e := r.db.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+$4::interval,updated_at=clock_timestamp() WHERE id=$1 AND status='running' AND locked_by=$2 AND lease_token=$3`, lease.Job.ID, lease.WorkerID, lease.Token, duration.String())
	if e != nil {
		return fmt.Errorf("extend job lease: %w", e)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleJobLease
	}
	return nil
}
func (r *Repository) Complete(ctx context.Context, lease Lease) error {
	return r.fence(ctx, lease, `UPDATE jobs SET status='completed',completed_at=clock_timestamp(),updated_at=clock_timestamp(),locked_by=NULL,locked_at=NULL,lease_expires_at=NULL,lease_token=NULL WHERE id=$1 AND status='running' AND locked_by=$2 AND lease_token=$3`)
}
func (r *Repository) RecoverExpired(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("recovery limit must be positive")
	}
	tag, e := r.db.Exec(ctx, `WITH expired AS (SELECT id FROM jobs WHERE status='running' AND lease_expires_at<clock_timestamp() ORDER BY lease_expires_at FOR UPDATE SKIP LOCKED LIMIT $1) UPDATE jobs j SET status='pending',run_at=clock_timestamp(),updated_at=clock_timestamp(),locked_by=NULL,locked_at=NULL,lease_expires_at=NULL,lease_token=NULL FROM expired WHERE j.id=expired.id`, limit)
	if e != nil {
		return 0, fmt.Errorf("recover expired jobs: %w", e)
	}
	return int(tag.RowsAffected()), nil
}
func (r *Repository) Reschedule(ctx context.Context, lease Lease, failure *JobError, delay time.Duration) error {
	if failure == nil {
		failure = &JobError{Class: Transient, Code: "internal_error", Message: "job failed"}
	}
	code, msg := sanitize(failure.Code, failure.Message)
	status := Pending
	if failure.Class == Permanent || lease.Job.Attempts >= lease.Job.MaxAttempts {
		status = Dead
		delay = 0
	}
	sql := `UPDATE jobs SET status=$4,run_at=CASE WHEN $4='pending' THEN clock_timestamp()+$5::interval ELSE run_at END,last_error_code=$6,last_error_message=$7,updated_at=clock_timestamp(),locked_by=NULL,locked_at=NULL,lease_expires_at=NULL,lease_token=NULL WHERE id=$1 AND status='running' AND locked_by=$2 AND lease_token=$3`
	tag, e := r.db.Exec(ctx, sql, lease.Job.ID, lease.WorkerID, lease.Token, status, delay.String(), code, msg)
	if e != nil {
		return fmt.Errorf("reschedule job: %w", e)
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleJobLease
	}
	return nil
}
func (r *Repository) fence(ctx context.Context, lease Lease, query string) error {
	tag, e := r.db.Exec(ctx, query, lease.Job.ID, lease.WorkerID, lease.Token)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return ErrStaleJobLease
	}
	return nil
}
func sanitize(code, msg string) (string, string) {
	code = strings.TrimSpace(code)
	if code == "" {
		code = "internal_error"
	}
	if len(code) > 64 {
		code = code[:64]
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "job failed"
	}
	if len(msg) > 256 {
		msg = msg[:256]
	}
	return code, msg
}

// RunReceipt commits the receipt and a DB-only handler effect in one transaction.
// It reports whether the handler made a new effect; a preexisting receipt is safe.
func (r *Repository) RunReceipt(ctx context.Context, lease Lease, handler Handler) (bool, error) {
	if lease.Job.EventID == nil {
		return false, fmt.Errorf("job has no event")
	}
	var applied bool
	err := r.db.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `INSERT INTO event_handler_receipts(event_id,handler) VALUES($1,$2) ON CONFLICT DO NOTHING`, *lease.Job.EventID, lease.Job.Handler)
		if e != nil {
			return fmt.Errorf("write receipt: %w", e)
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		var event DomainEvent
		e = tx.QueryRow(ctx, `SELECT id,event_type,payload,correlation_id,causation_id,occurred_at FROM outbox_events WHERE id=$1`, *lease.Job.EventID).Scan(&event.ID, &event.Type, &event.Payload, &event.CorrelationID, &event.CausationID, &event.OccurredAt)
		if e != nil {
			return fmt.Errorf("load event: %w", e)
		}
		if e = handler.Handle(ctx, tx, event); e != nil {
			return e
		}
		applied = true
		return nil
	})
	return applied, err
}

type WorkerConfig struct {
	WorkerID                     string
	PollInterval, LeaseDuration  time.Duration
	DispatchBatch, RecoveryBatch int
}

type Worker struct {
	dispatcher *Dispatcher
	repository *Repository
	registry   *Registry
	config     WorkerConfig
}

func NewWorker(dispatcher *Dispatcher, repository *Repository, registry *Registry, config WorkerConfig) (*Worker, error) {
	if dispatcher == nil || repository == nil || registry == nil || strings.TrimSpace(config.WorkerID) == "" || config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.DispatchBatch <= 0 || config.RecoveryBatch <= 0 {
		return nil, fmt.Errorf("invalid worker configuration")
	}
	return &Worker{dispatcher: dispatcher, repository: repository, registry: registry, config: config}, nil
}

// Run stops dispatching and claiming as soon as ctx is cancelled. Claimed work
// is deliberately left unfinished; PostgreSQL expiry recovery makes it eligible.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.config.PollInterval)
	defer ticker.Stop()
	for {
		if err := w.Poll(ctx); err != nil && ctx.Err() == nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (w *Worker) Poll(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}
	if _, err := w.repository.RecoverExpired(ctx, w.config.RecoveryBatch); err != nil {
		return err
	}
	if _, err := w.dispatcher.DispatchBatch(ctx, w.config.DispatchBatch); err != nil {
		return err
	}
	lease, err := w.repository.Claim(ctx, w.config.WorkerID, w.config.LeaseDuration)
	if err != nil || lease == nil {
		return err
	}
	return w.handle(ctx, *lease)
}

func (w *Worker) handle(ctx context.Context, lease Lease) error {
	handler, ok := w.registry.Resolve(lease.Job.Handler)
	if !ok {
		return w.repository.Reschedule(ctx, lease, &JobError{Class: Permanent, Code: "unknown_handler", Message: "registered handler unavailable"}, 0)
	}
	_, err := w.repository.RunReceipt(ctx, lease, handler)
	if err == nil {
		return w.repository.Complete(ctx, lease)
	}
	if ctx.Err() != nil {
		return nil
	}
	var typed *JobError
	if !errors.As(err, &typed) {
		typed = &JobError{Class: Transient, Code: "internal_error", Message: "job handler failed", Cause: err}
	}
	delay := retryDelay(lease.Job.Attempts, typed.RetryAfter)
	return w.repository.Reschedule(ctx, lease, typed, delay)
}

func retryDelay(attempts int, retryAfter *time.Duration) time.Duration {
	// The no-jitter default is deterministic and still avoids a busy retry loop.
	delay := time.Second
	for i := 1; i < attempts && delay < time.Minute; i++ {
		delay *= 2
	}
	if delay > time.Minute {
		delay = time.Minute
	}
	if retryAfter != nil && *retryAfter > delay {
		delay = *retryAfter
		if delay > time.Minute {
			delay = time.Minute
		}
	}
	return delay
}
