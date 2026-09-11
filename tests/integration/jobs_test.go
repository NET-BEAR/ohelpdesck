package integration

import (
	"context"
	"errors"
	"os"
	"strings"
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
		_, _ = pool.Exec(context.Background(), `DELETE FROM jobs WHERE event_id IS NULL AND handler='test.handler'`)
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
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code,last_error_message FROM jobs WHERE id=$1`, id).Scan(&status, &code, &msg); err != nil || status != jobs.Dead || code != "exhausted" || msg != "job retry exhausted" {
		t.Fatalf("status=%s code=%s msg=%s err=%v", status, code, msg, err)
	}
}

type failingReceiptHandler struct{ err error }

func (h failingReceiptHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error { return h.err }

type postCommitReceiptHandler struct {
	calls      atomic.Int32
	afterCalls atomic.Int32
	afterErr   error
}

func (h *postCommitReceiptHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.calls.Add(1)
	return nil
}

func (h *postCommitReceiptHandler) AfterCommit(_ context.Context, event jobs.DomainEvent) error {
	if event.ID == uuid.Nil {
		return errors.New("missing event")
	}
	h.afterCalls.Add(1)
	return h.afterErr
}

func TestJobsRepositoryLeaseOperationsAndFailurePolicies(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	if lease, err := repo.Claim(ctx, "", time.Second); err == nil || lease != nil {
		t.Fatalf("empty worker claim=%+v err=%v", lease, err)
	}
	if lease, err := repo.Claim(ctx, "worker", 0); err == nil || lease != nil {
		t.Fatalf("zero lease claim=%+v err=%v", lease, err)
	}
	if recovered, err := repo.RecoverExpired(ctx, 0); err == nil || recovered != 0 {
		t.Fatalf("zero recovery=%d err=%v", recovered, err)
	}
	id := insertJob(t, ctx, pool, 10)
	lease, err := repo.Claim(ctx, "worker-a", time.Minute)
	if err != nil || lease == nil || lease.Job.ID != id {
		t.Fatalf("claim=%+v err=%v", lease, err)
	}
	if err = repo.Extend(ctx, *lease, 0); err == nil {
		t.Fatal("zero extension accepted")
	}
	if err = repo.Extend(ctx, *lease, time.Minute); err != nil {
		t.Fatal(err)
	}
	longCode := strings.Repeat("x", 80)
	longMessage := strings.Repeat("m", 300)
	if err = repo.Reschedule(ctx, *lease, &jobs.JobError{Class: jobs.Transient, Code: longCode, Message: longMessage}, time.Second); err != nil {
		t.Fatal(err)
	}
	var status jobs.Status
	var code, message string
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code,last_error_message FROM jobs WHERE id=$1`, id).Scan(&status, &code, &message); err != nil {
		t.Fatal(err)
	}
	if status != jobs.Pending || code != "internal_error" || message != "job failed" {
		t.Fatalf("rescheduled status=%s code=%d message=%d", status, len(code), len(message))
	}
	if err = repo.Complete(ctx, *lease); !errors.Is(err, jobs.ErrStaleJobLease) {
		t.Fatalf("stale completion=%v", err)
	}
	if err = repo.Reschedule(ctx, *lease, nil, time.Second); !errors.Is(err, jobs.ErrStaleJobLease) {
		t.Fatalf("stale reschedule=%v", err)
	}

	defaultID := insertJob(t, ctx, pool, 10)
	defaultLease, err := repo.Claim(ctx, "worker-default", time.Minute)
	if err != nil || defaultLease == nil || defaultLease.Job.ID != defaultID {
		t.Fatalf("default claim=%+v err=%v", defaultLease, err)
	}
	if err = repo.Reschedule(ctx, *defaultLease, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code,last_error_message FROM jobs WHERE id=$1`, defaultID).Scan(&status, &code, &message); err != nil || status != jobs.Pending || code != "internal_error" || message != "job failed" {
		t.Fatalf("default failure status=%s code=%s message=%s err=%v", status, code, message, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, defaultID); err != nil {
		t.Fatal(err)
	}
	permanentLease, err := repo.Claim(ctx, "worker-permanent", time.Minute)
	if err != nil || permanentLease == nil {
		t.Fatalf("permanent claim=%+v err=%v", permanentLease, err)
	}
	if err = repo.Reschedule(ctx, *permanentLease, &jobs.JobError{Class: jobs.Permanent, Code: "rejected", Message: "do not retry"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id=$1`, defaultID).Scan(&status); err != nil || status != jobs.Dead {
		t.Fatalf("permanent status=%s err=%v", status, err)
	}
}

func TestJobsWorkerPollCompletesAndHandlesFailures(t *testing.T) {
	ctx, pool := jobsFixture(t)
	event := jobEvent(t, ctx, pool, "jobs.worker")
	h := &receiptHandler{}
	r := registry(t, h, "jobs.worker", "worker.ok")
	dispatcher := jobs.NewDispatcher(pool, r)
	repo := jobs.NewRepository(pool)
	worker, err := jobs.NewWorker(dispatcher, repo, r, jobs.WorkerConfig{WorkerID: "worker-ok", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler calls=%d", h.calls.Load())
	}
	var status jobs.Status
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='worker.ok'`, event).Scan(&status); err != nil || status != jobs.Completed {
		t.Fatalf("completed status=%s err=%v", status, err)
	}

	failedEvent := jobEvent(t, ctx, pool, "jobs.fail")
	failing := failingReceiptHandler{err: errors.New("handler down")}
	failRegistry := registry(t, failing, "jobs.fail", "worker.fail")
	failWorker, err := jobs.NewWorker(jobs.NewDispatcher(pool, failRegistry), repo, failRegistry, jobs.WorkerConfig{WorkerID: "worker-fail", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err = failWorker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var failedStatus jobs.Status
	var errorCode string
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code FROM jobs WHERE event_id=$1 AND handler='worker.fail'`, failedEvent).Scan(&failedStatus, &errorCode); err != nil || failedStatus != jobs.Pending || errorCode != "internal_error" {
		t.Fatalf("failed status=%s code=%s err=%v", failedStatus, errorCode, err)
	}
}

func TestJobsWorkerUnknownHandlerAndLifecycleValidation(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	id := insertJob(t, ctx, pool, 1)
	r := jobs.NewRegistry()
	worker, err := jobs.NewWorker(jobs.NewDispatcher(pool, r), repo, r, jobs.WorkerConfig{WorkerID: "worker-unknown", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 1, RecoveryBatch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err = worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var status jobs.Status
	var code string
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code FROM jobs WHERE id=$1`, id).Scan(&status, &code); err != nil || status != jobs.Dead || code != "unknown_handler" {
		t.Fatalf("unknown handler status=%s code=%s err=%v", status, code, err)
	}
	if _, err = jobs.NewWorker(nil, repo, r, jobs.WorkerConfig{WorkerID: "x", PollInterval: time.Millisecond, LeaseDuration: time.Second, DispatchBatch: 1, RecoveryBatch: 1}); err == nil {
		t.Fatal("nil dispatcher accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = worker.Poll(cancelled); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(cancelled) }()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestJobsWorkerPostCommitEffectAndFailureRetry(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)

	successEvent := jobEvent(t, ctx, pool, "jobs.postcommit.success")
	success := &postCommitReceiptHandler{}
	successRegistry := registry(t, success, "jobs.postcommit.success", "postcommit.success")
	successWorker, err := jobs.NewWorker(jobs.NewDispatcher(pool, successRegistry), repo, successRegistry, jobs.WorkerConfig{
		WorkerID: "worker-postcommit-success", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = successWorker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if success.calls.Load() != 1 || success.afterCalls.Load() != 1 {
		t.Fatalf("handler=%d after=%d", success.calls.Load(), success.afterCalls.Load())
	}
	loaded, err := repo.Event(ctx, successEvent)
	if err != nil || loaded.ID != successEvent || loaded.Type != "jobs.postcommit.success" {
		t.Fatalf("loaded event=%+v err=%v", loaded, err)
	}
	var status jobs.Status
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='postcommit.success'`, successEvent).Scan(&status); err != nil || status != jobs.Completed {
		t.Fatalf("success status=%s err=%v", status, err)
	}

	failureEvent := jobEvent(t, ctx, pool, "jobs.postcommit.failure")
	failure := &postCommitReceiptHandler{afterErr: errors.New("session unavailable")}
	failureRegistry := registry(t, failure, "jobs.postcommit.failure", "postcommit.failure")
	failureWorker, err := jobs.NewWorker(jobs.NewDispatcher(pool, failureRegistry), repo, failureRegistry, jobs.WorkerConfig{
		WorkerID: "worker-postcommit-failure", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = failureWorker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if failure.calls.Load() != 1 || failure.afterCalls.Load() != 1 {
		t.Fatalf("failed handler=%d after=%d", failure.calls.Load(), failure.afterCalls.Load())
	}
	var code string
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code FROM jobs WHERE event_id=$1 AND handler='postcommit.failure'`, failureEvent).Scan(&status, &code); err != nil || status != jobs.Pending || code != "internal_error" {
		t.Fatalf("failure status=%s code=%s err=%v", status, code, err)
	}
}

func TestJobsRepositoryEventNotFoundAndRecoverMultipleExpired(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	if _, err := repo.Event(ctx, uuid.New()); err == nil {
		t.Fatal("missing event loaded")
	}

	for _, priority := range []int{20, 10} {
		id := insertJob(t, ctx, pool, priority)
		lease, err := repo.Claim(ctx, "expired-worker", time.Minute)
		if err != nil || lease == nil || lease.Job.ID != id {
			t.Fatalf("lease=%+v err=%v id=%s", lease, err, id)
		}
		if _, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if recovered, err := repo.RecoverExpired(ctx, 1); err != nil || recovered != 1 {
		t.Fatalf("first recovery=%d err=%v", recovered, err)
	}
	if recovered, err := repo.RecoverExpired(ctx, 10); err != nil || recovered != 1 {
		t.Fatalf("second recovery=%d err=%v", recovered, err)
	}
}

func TestJobsReceiptRejectsEventlessLease(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	if applied, err := repo.RunReceipt(ctx, jobs.Lease{Job: jobs.Job{Handler: "receipt.handler"}}, &receiptHandler{}); err == nil || applied {
		t.Fatalf("eventless receipt applied=%v err=%v", applied, err)
	}
}

func TestJobsRegistryAndDispatcherInputValidation(t *testing.T) {
	r := jobs.NewRegistry()
	if err := r.Register(jobs.Route{}, &receiptHandler{}); err == nil {
		t.Fatal("blank route accepted")
	}
	if err := r.Register(jobs.Route{EventType: "event", Handler: "handler"}, nil); err == nil {
		t.Fatal("nil handler accepted")
	}
	h := &receiptHandler{}
	if err := r.Register(jobs.Route{EventType: " event ", Handler: " handler "}, h); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(jobs.Route{EventType: "event", Handler: "handler"}, h); err == nil {
		t.Fatal("duplicate handler accepted")
	}
	if got := r.EventTypes(); len(got) != 1 || got[0] != "event" {
		t.Fatalf("event types=%v", got)
	}
	if got := r.Routes("event"); len(got) != 1 || got[0].Handler != "handler" {
		t.Fatalf("routes=%+v", got)
	}
	if _, ok := r.Resolve("missing"); ok {
		t.Fatal("unknown handler resolved")
	}
	ctx, pool := jobsFixture(t)
	if _, err := jobs.NewDispatcher(pool, jobs.NewRegistry()).DispatchBatch(ctx, 0); err == nil {
		t.Fatal("zero batch accepted")
	}
	result, err := jobs.NewDispatcher(pool, jobs.NewRegistry()).DispatchBatch(ctx, 1)
	if err != nil || result.Events != 0 || result.Jobs != 0 {
		t.Fatalf("empty dispatch=%+v err=%v", result, err)
	}
}

type retryAfterHandler struct {
	calls atomic.Int32
	delay time.Duration
}

func (h *retryAfterHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.calls.Add(1)
	return &jobs.JobError{Class: jobs.Transient, Code: "upstream_busy", Message: "retry later", RetryAfter: &h.delay}
}

type cancelingHandler struct{ cancel context.CancelFunc }

func (h cancelingHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.cancel()
	return errors.New("interrupted during shutdown")
}

func TestJobsWorkerUsesRetryAfterAndLeavesCancelledWorkForRecovery(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)

	retryEvent := jobEvent(t, ctx, pool, "jobs.retry-after")
	retry := &retryAfterHandler{delay: 3 * time.Second}
	retryRegistry := registry(t, retry, "jobs.retry-after", "retry.after")
	worker, err := jobs.NewWorker(jobs.NewDispatcher(pool, retryRegistry), repo, retryRegistry, jobs.WorkerConfig{
		WorkerID: "worker-retry-after", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(2500 * time.Millisecond)
	if err = worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var status jobs.Status
	var runAt time.Time
	if err = pool.QueryRow(ctx, `SELECT status,run_at FROM jobs WHERE event_id=$1 AND handler='retry.after'`, retryEvent).Scan(&status, &runAt); err != nil {
		t.Fatal(err)
	}
	if retry.calls.Load() != 1 || status != jobs.Pending || runAt.Before(before) {
		t.Fatalf("calls=%d status=%s run_at=%s before=%s", retry.calls.Load(), status, runAt, before)
	}

	cancelEvent := jobEvent(t, ctx, pool, "jobs.cancelled-handler")
	pollCtx, cancel := context.WithCancel(ctx)
	cancelRegistry := registry(t, cancelingHandler{cancel: cancel}, "jobs.cancelled-handler", "cancel.handler")
	cancelWorker, err := jobs.NewWorker(jobs.NewDispatcher(pool, cancelRegistry), repo, cancelRegistry, jobs.WorkerConfig{
		WorkerID: "worker-cancel", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = cancelWorker.Poll(pollCtx); err != nil {
		t.Fatal(err)
	}
	var lockedStatus jobs.Status
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='cancel.handler'`, cancelEvent).Scan(&lockedStatus); err != nil {
		t.Fatal(err)
	}
	if lockedStatus != jobs.Running {
		t.Fatalf("cancelled work status=%s", lockedStatus)
	}
}

func TestJobsWorkerRunPollsUntilCancellation(t *testing.T) {
	ctx, pool := jobsFixture(t)
	event := jobEvent(t, ctx, pool, "jobs.run")
	h := &receiptHandler{}
	r := registry(t, h, "jobs.run", "run.handler")
	worker, err := jobs.NewWorker(jobs.NewDispatcher(pool, r), jobs.NewRepository(pool), r, jobs.WorkerConfig{
		WorkerID: "worker-run", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(runCtx) }()
	deadline := time.Now().Add(time.Second)
	var status jobs.Status
	for time.Now().Before(deadline) {
		err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='run.handler'`, event).Scan(&status)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		if err == nil && status == jobs.Completed {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if status != jobs.Completed {
		t.Fatalf("run did not complete before cancellation: status=%s", status)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if h.calls.Load() != 1 {
		t.Fatalf("handler calls=%d", h.calls.Load())
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='run.handler'`, event).Scan(&status); err != nil || status != jobs.Completed {
		t.Fatalf("run status=%s err=%v", status, err)
	}
}

type permanentHandler struct{ calls atomic.Int32 }

func (h *permanentHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.calls.Add(1)
	return &jobs.JobError{Class: jobs.Permanent, Code: "rejected", Message: "recipient rejected"}
}

type longRetryHandler struct {
	calls atomic.Int32
	delay time.Duration
}

func (h *longRetryHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.calls.Add(1)
	return &jobs.JobError{Class: jobs.Transient, Code: "upstream_timeout", Message: "upstream timeout", RetryAfter: &h.delay}
}

func TestJobsReceiptRollsBackHandlerFailureAndPreservesRetryability(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	event := jobEvent(t, ctx, pool, "jobs.receipt.rollback")
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,handler,event_id) VALUES($1,'domain_event','receipt.rollback',$2)`, id, event); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Claim(ctx, "receipt-rollback", time.Minute)
	if err != nil || lease == nil {
		t.Fatalf("claim=%+v err=%v", lease, err)
	}
	if applied, err := repo.RunReceipt(ctx, *lease, failingReceiptHandler{err: errors.New("database effect rejected")}); err == nil || applied {
		t.Fatalf("failed receipt applied=%v err=%v", applied, err)
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM event_handler_receipts WHERE event_id=$1 AND handler='receipt.rollback'`, event).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("rolled back receipts=%d err=%v", receipts, err)
	}
	ok := &receiptHandler{}
	if applied, err := repo.RunReceipt(ctx, *lease, ok); err != nil || !applied || ok.calls.Load() != 1 {
		t.Fatalf("retried receipt applied=%v calls=%d err=%v", applied, ok.calls.Load(), err)
	}
	if err := repo.Complete(ctx, *lease); err != nil {
		t.Fatal(err)
	}
}

func TestJobsWorkerPermanentAndCappedRetryPolicies(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)

	permanentEvent := jobEvent(t, ctx, pool, "jobs.worker.permanent")
	permanent := &permanentHandler{}
	permanentRegistry := registry(t, permanent, "jobs.worker.permanent", "worker.permanent")
	permanentWorker, err := jobs.NewWorker(jobs.NewDispatcher(pool, permanentRegistry), repo, permanentRegistry, jobs.WorkerConfig{
		WorkerID: "worker-permanent-policy", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = permanentWorker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var status jobs.Status
	var code string
	if err = pool.QueryRow(ctx, `SELECT status,last_error_code FROM jobs WHERE event_id=$1 AND handler='worker.permanent'`, permanentEvent).Scan(&status, &code); err != nil || status != jobs.Dead || code != "rejected" || permanent.calls.Load() != 1 {
		t.Fatalf("permanent status=%s code=%s calls=%d err=%v", status, code, permanent.calls.Load(), err)
	}

	retryEvent := jobEvent(t, ctx, pool, "jobs.worker.capped-retry")
	longRetry := &longRetryHandler{delay: 2 * time.Minute}
	retryRegistry := registry(t, longRetry, "jobs.worker.capped-retry", "worker.capped-retry")
	retryWorker, err := jobs.NewWorker(jobs.NewDispatcher(pool, retryRegistry), repo, retryRegistry, jobs.WorkerConfig{
		WorkerID: "worker-capped-retry", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(55 * time.Second)
	if err = retryWorker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var runAt time.Time
	if err = pool.QueryRow(ctx, `SELECT status,run_at FROM jobs WHERE event_id=$1 AND handler='worker.capped-retry'`, retryEvent).Scan(&status, &runAt); err != nil || status != jobs.Pending || runAt.Before(before) || longRetry.calls.Load() != 1 {
		t.Fatalf("retry status=%s runAt=%s calls=%d err=%v", status, runAt, longRetry.calls.Load(), err)
	}
}

type exponentialRetryHandler struct{ calls atomic.Int32 }

func (h *exponentialRetryHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	h.calls.Add(1)
	return &jobs.JobError{Class: jobs.Transient, Code: "transient", Message: "retry with exponential backoff"}
}

func TestJobsWorkerCapsExponentialBackoffAtOneMinute(t *testing.T) {
	ctx, pool := jobsFixture(t)
	event := jobEvent(t, ctx, pool, "jobs.worker.exponential-retry")
	h := &exponentialRetryHandler{}
	r := registry(t, h, "jobs.worker.exponential-retry", "worker.exponential-retry")
	worker, err := jobs.NewWorker(jobs.NewDispatcher(pool, r), jobs.NewRepository(pool), r, jobs.WorkerConfig{
		WorkerID: "worker-exponential-retry", PollInterval: time.Millisecond, LeaseDuration: time.Minute, DispatchBatch: 10, RecoveryBatch: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE outbox_events SET occurred_at=clock_timestamp()-interval '1 minute' WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	if err = worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var jobID uuid.UUID
	if err = pool.QueryRow(ctx, `SELECT id FROM jobs WHERE event_id=$1 AND handler='worker.exponential-retry'`, event).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET status='pending',attempts=6,run_at=clock_timestamp(),locked_by=NULL,locked_at=NULL,lease_expires_at=NULL,lease_token=NULL WHERE id=$1`, jobID); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC().Add(55 * time.Second)
	if err = worker.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	var status jobs.Status
	var runAt time.Time
	if err = pool.QueryRow(ctx, `SELECT status,run_at FROM jobs WHERE id=$1`, jobID).Scan(&status, &runAt); err != nil || status != jobs.Pending || runAt.Before(before) || h.calls.Load() != 2 {
		t.Fatalf("status=%s runAt=%s calls=%d err=%v", status, runAt, h.calls.Load(), err)
	}
}

func TestJobsSanitizesBlankFailureAndLeaseExtensionFencing(t *testing.T) {
	var nilError *jobs.JobError
	if got := nilError.Error(); got != "" {
		t.Fatalf("nil JobError text=%q", got)
	}
	if got := (&jobs.JobError{Message: "descriptive"}).Error(); got != "descriptive" {
		t.Fatalf("JobError text=%q", got)
	}

	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	id := insertJob(t, ctx, pool, 1)
	lease, err := repo.Claim(ctx, "sanitization-worker", time.Minute)
	if err != nil || lease == nil || lease.Job.ID != id {
		t.Fatalf("claim=%+v err=%v", lease, err)
	}
	if err = repo.Reschedule(ctx, *lease, &jobs.JobError{}, time.Second); err != nil {
		t.Fatal(err)
	}
	var code, message string
	if err = pool.QueryRow(ctx, `SELECT last_error_code,last_error_message FROM jobs WHERE id=$1`, id).Scan(&code, &message); err != nil || code != "internal_error" || message != "job failed" {
		t.Fatalf("sanitized code=%q message=%q err=%v", code, message, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET run_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	fresh, err := repo.Claim(ctx, "fresh-worker", time.Minute)
	if err != nil || fresh == nil {
		t.Fatalf("fresh claim=%+v err=%v", fresh, err)
	}
	if err = repo.Extend(ctx, *lease, time.Minute); !errors.Is(err, jobs.ErrStaleJobLease) {
		t.Fatalf("stale extend=%v", err)
	}
	if err = repo.Complete(ctx, *fresh); err != nil {
		t.Fatal(err)
	}

	secretID := insertJob(t, ctx, pool, 1)
	secretLease, err := repo.Claim(ctx, "redaction-worker", time.Minute)
	if err != nil || secretLease == nil || secretLease.Job.ID != secretID {
		t.Fatalf("redaction claim=%+v err=%v", secretLease, err)
	}
	if err = repo.Reschedule(ctx, *secretLease, &jobs.JobError{Class: jobs.Transient, Code: "A7q9M2xV4kL8rP3z", Message: "Z8v2Q5mR9xK4pL7t", Cause: errors.New("N6s1D8yW3cF5hJ0b")}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT last_error_code,last_error_message FROM jobs WHERE id=$1`, secretID).Scan(&code, &message); err != nil {
		t.Fatal(err)
	}
	if code != "internal_error" || message != "job failed" || strings.Contains(strings.ToLower(code+message), "a7q9m2xv4kl8rp3z") || strings.Contains(strings.ToLower(code+message), "z8v2q5mr9xk4pl7t") || strings.Contains(strings.ToLower(code+message), "n6s1d8yw3cf5hj0b") {
		t.Fatalf("persisted sensitive error code=%q message=%q", code, message)
	}
}

func TestJobsRecoveryExhaustedLeaseMarksDeadAndNeverReclaims(t *testing.T) {
	ctx, pool := jobsFixture(t)
	repo := jobs.NewRepository(pool)
	id := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO jobs(id,type,handler,max_attempts) VALUES($1,'domain_event','recovery.exhausted',1)`, id); err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Claim(ctx, "worker-exhausted", time.Minute)
	if err != nil || lease == nil || lease.Job.ID != id || lease.Job.Attempts != 1 {
		t.Fatalf("initial claim=%+v err=%v", lease, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if recovered, err := repo.RecoverExpired(ctx, 10); err != nil || recovered != 1 {
		t.Fatalf("recovery=%d err=%v", recovered, err)
	}
	var status jobs.Status
	var attempts int
	if err = pool.QueryRow(ctx, `SELECT status,attempts FROM jobs WHERE id=$1`, id).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != jobs.Dead || attempts != 1 {
		t.Fatalf("recovered status=%s attempts=%d", status, attempts)
	}
	if next, err := repo.Claim(ctx, "worker-next", time.Minute); err != nil || next != nil {
		t.Fatalf("exhausted job reclaimed=%+v err=%v", next, err)
	}
}
