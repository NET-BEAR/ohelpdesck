package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type registryHandler struct{}

func (registryHandler) Handle(context.Context, pgx.Tx, DomainEvent) error {
	return nil
}

func TestRegistryRegistrationRoutesAndResolution(t *testing.T) {
	r := NewRegistry()
	h := registryHandler{}
	if err := r.Register(Route{EventType: "message.queued", Handler: "handler.z"}, h); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Route{EventType: "message.queued", Handler: "handler.a"}, h); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Route{EventType: "conversation.opened", Handler: "handler.open"}, registryHandler{}); err != nil {
		t.Fatal(err)
	}
	if got, want := r.EventTypes(), []string{"conversation.opened", "message.queued"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	routes := r.Routes("message.queued")
	if len(routes) != 2 || routes[0].Handler != "handler.a" || routes[1].Handler != "handler.z" {
		t.Fatalf("routes = %+v", routes)
	}
	routes[0].Handler = "caller-mutation"
	if r.Routes("message.queued")[0].Handler != "handler.a" {
		t.Fatal("Routes returned registry-owned slice")
	}
	if resolved, ok := r.Resolve("handler.z"); !ok || resolved == nil {
		t.Fatalf("handler.z resolution = %v, %v", resolved, ok)
	}
	if _, ok := r.Resolve("missing"); ok {
		t.Fatal("unknown handler resolved")
	}
}

func TestRegistryRejectsInvalidAndDuplicateHandlers(t *testing.T) {
	r := NewRegistry()
	for _, route := range []Route{{}, {EventType: "event"}, {Handler: "handler"}} {
		if err := r.Register(route, registryHandler{}); err == nil {
			t.Fatalf("invalid route %+v accepted", route)
		}
	}
	if err := r.Register(Route{EventType: "event", Handler: "handler"}, nil); err == nil {
		t.Fatal("nil handler accepted")
	}
	if err := r.Register(Route{EventType: "event", Handler: "handler"}, registryHandler{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Route{EventType: "other", Handler: "handler"}, registryHandler{}); err == nil {
		t.Fatal("duplicate handler accepted")
	}
}

func TestJobErrorAndSanitize(t *testing.T) {
	if (*JobError)(nil).Error() != "" {
		t.Fatal("nil JobError should have empty error string")
	}
	if got := (&JobError{Message: "retry later"}).Error(); got != "retry later" {
		t.Fatalf("error = %q", got)
	}
	code, message := sanitize("  ", "\t")
	if code != "internal_error" || message != "job failed" {
		t.Fatalf("defaults = %q, %q", code, message)
	}
	code, message = sanitize(string(make([]byte, 70)), string(make([]byte, 270)))
	if len(code) != 64 || len(message) != 256 {
		t.Fatalf("lengths = %d, %d", len(code), len(message))
	}
}

func TestRetryDelayCapsAndRespectsLongerProviderHint(t *testing.T) {
	fiveSeconds := 5 * time.Second
	twoMinutes := 2 * time.Minute
	cases := []struct {
		name       string
		attempts   int
		retryAfter *time.Duration
		want       time.Duration
	}{
		{name: "first retry", attempts: 1, want: time.Second},
		{name: "exponential", attempts: 4, want: 8 * time.Second},
		{name: "maximum", attempts: 10, want: time.Minute},
		{name: "provider hint", attempts: 1, retryAfter: &fiveSeconds, want: fiveSeconds},
		{name: "provider hint capped", attempts: 1, retryAfter: &twoMinutes, want: time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryDelay(tc.attempts, tc.retryAfter); got != tc.want {
				t.Fatalf("retryDelay = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNewWorkerRejectsIncompleteConfiguration(t *testing.T) {
	registry := NewRegistry()
	if _, err := NewWorker(nil, nil, registry, WorkerConfig{}); err == nil {
		t.Fatal("incomplete worker configuration accepted")
	}
	worker, err := NewWorker(&Dispatcher{}, &Repository{}, registry, WorkerConfig{
		WorkerID: "worker-1", PollInterval: time.Second, LeaseDuration: time.Minute, DispatchBatch: 1, RecoveryBatch: 1,
	})
	if err != nil || worker == nil {
		t.Fatalf("valid worker = %v, %v", worker, err)
	}
}

func TestWorkerCancellationDoesNotTouchPersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &Worker{config: WorkerConfig{PollInterval: time.Millisecond}}
	if err := w.Poll(ctx); err != nil {
		t.Fatalf("cancelled poll = %v", err)
	}
	if err := w.Run(ctx); err != nil {
		t.Fatalf("cancelled run = %v", err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("test context was not cancelled")
	}
}
