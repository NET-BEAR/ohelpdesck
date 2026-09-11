package integration

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/jobs"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type receiptHandler struct{ calls atomic.Int32 }

func (h *receiptHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.calls.Add(1)
	return nil
}

func jobsFixture(t *testing.T) (context.Context, *database.Pool) {
	t.Helper()
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err = pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE aggregate_type='jobs-integration'`)
	})
	return ctx, pool
}
func jobEvent(t *testing.T, ctx context.Context, pool *database.Pool, typ string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,payload_version,correlation_id,occurred_at) VALUES($1,'jobs-integration',$2,$3,'{}',1,$4,clock_timestamp())`, id, uuid.New(), typ, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func insertJob(t *testing.T, ctx context.Context, pool *database.Pool, priority int) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,handler,priority) VALUES($1,'domain_event','test.handler',$2)`, id, priority); err != nil {
		t.Fatal(err)
	}
	return id
}
func registry(t *testing.T, handler jobs.Handler, typ string, names ...string) *jobs.Registry {
	t.Helper()
	r := jobs.NewRegistry()
	for _, n := range names {
		if err := r.Register(jobs.Route{EventType: typ, Handler: n}, handler); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestJobsDispatcherFanoutIdempotencyAndUnknownRetention(t *testing.T) {
	ctx, pool := jobsFixture(t)
	handler := &receiptHandler{}
	r := registry(t, handler, "jobs.event", "handler.b", "handler.a")
	known := jobEvent(t, ctx, pool, "jobs.event")
	unknown := jobEvent(t, ctx, pool, "jobs.unknown")
	d := jobs.NewDispatcher(pool, r)
	result, err := d.DispatchBatch(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Events != 1 || result.Jobs != 2 {
		t.Fatalf("dispatch=%+v", result)
	}
	var jobsCount int
	var dispatched, unknownDispatched *time.Time
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE event_id=$1`, known).Scan(&jobsCount); err != nil || jobsCount != 2 {
		t.Fatalf("jobs=%d err=%v", jobsCount, err)
	}
	if err = pool.QueryRow(ctx, `SELECT dispatched_at FROM outbox_events WHERE id=$1`, known).Scan(&dispatched); err != nil || dispatched == nil {
		t.Fatalf("known dispatched=%v err=%v", dispatched, err)
	}
	if err = pool.QueryRow(ctx, `SELECT dispatched_at FROM outbox_events WHERE id=$1`, unknown).Scan(&unknownDispatched); err != nil || unknownDispatched != nil {
		t.Fatalf("unknown dispatched=%v err=%v", unknownDispatched, err)
	}
	if result, err = d.DispatchBatch(ctx, 10); err != nil || result.Jobs != 0 {
		t.Fatalf("repeat=%+v err=%v", result, err)
	}
}
func TestJobsConcurrentDispatcherCreatesCanonicalFanout(t *testing.T) {
	ctx, pool := jobsFixture(t)
	r := registry(t, &receiptHandler{}, "jobs.race", "handler")
	event := jobEvent(t, ctx, pool, "jobs.race")
	d := jobs.NewDispatcher(pool, r)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; _, errs[i] = d.DispatchBatch(ctx, 1) }(i)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("dispatch errors=%v", errs)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE event_id=$1 AND handler='handler'`, event).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs=%d err=%v", n, err)
	}
}
func TestJobsClaimFencingRecoveryAndEligibility(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	due := insertJob(t, ctx, pool, 100)
	future := insertJob(t, ctx, pool, 1)
	if _, err := pool.Exec(ctx, `UPDATE jobs SET run_at=clock_timestamp()+interval '1 hour' WHERE id=$1`, future); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	leases := make([]*jobs.Lease, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range leases {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			leases[i], errs[i] = repo.Claim(ctx, "worker-"+string(rune('a'+i)), time.Minute)
		}(i)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	var first *jobs.Lease
	for _, l := range leases {
		if l != nil {
			first = l
		}
	}
	if first == nil || first.Job.ID != due || first.Job.Attempts != 1 {
		t.Fatalf("leases=%+v", leases)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, due); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.RecoverExpired(ctx, 10); err != nil || n != 1 {
		t.Fatalf("recovery=%d err=%v", n, err)
	}
	second, err := repo.Claim(ctx, "worker-c", time.Minute)
	if err != nil || second == nil || second.Token == first.Token {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	if err = repo.Complete(ctx, *first); !errors.Is(err, jobs.ErrStaleJobLease) {
		t.Fatalf("stale complete=%v", err)
	}
	if err = repo.Complete(ctx, *second); err != nil {
		t.Fatal(err)
	}
	if lease, err := repo.Claim(ctx, "worker-d", time.Minute); err != nil || lease != nil {
		t.Fatalf("future claim=%+v err=%v", lease, err)
	}
}
func TestJobsRetryDeadAndReceiptDeduplication(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	event := jobEvent(t, ctx, pool, "jobs.receipt")
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,handler,event_id,max_attempts) VALUES($1,'domain_event','receipt.handler',$2,2)`, id, event); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Claim(ctx, "worker-a", time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("claim=%+v err=%v", lease, err)
	}
	h := &receiptHandler{}
	applied, err := repo.RunReceipt(ctx, *lease, h)
	if err != nil || !applied {
		t.Fatalf("receipt=%v applied=%v", err, applied)
	}
	applied, err = repo.RunReceipt(ctx, *lease, h)
	if err != nil || applied || h.calls.Load() != 1 {
		t.Fatalf("dedup err=%v applied=%v calls=%d", err, applied, h.calls.Load())
	}
	if err = repo.Reschedule(ctx, *lease, &jobs.JobError{Class: jobs.Transient, Code: "retry", Message: "retry"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	lease, err = repo.Claim(ctx, "worker-b", time.Minute)
	if err != nil || lease == nil || lease.Job.Attempts != 2 {
		t.Fatalf("retry claim=%+v err=%v", lease, err)
	}
	if err = repo.Reschedule(ctx, *lease, &jobs.JobError{Class: jobs.Transient, Code: "exhausted", Message: "will be dead"}, time.Second); err != nil {
		t.Fatal(err)
	}
	var status jobs.Status
	var code, msg string
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code,last_error_message FROM jobs WHERE id=$1`, id).Scan(&status, &code, &msg); err != nil || status != jobs.Dead || code != "exhausted" || msg != "will be dead" {
		t.Fatalf("status=%s code=%s msg=%s err=%v", status, code, msg, err)
	}
}
