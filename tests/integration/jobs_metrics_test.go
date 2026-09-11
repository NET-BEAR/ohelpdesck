package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NET-BEAR/ohelpdesck/internal/jobs"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/telemetry"
)

func TestJobsMetricsExposeRetainedOutboxAndQueueDepth(t *testing.T) {
	ctx, pool := jobsFixture(t)
	unknown := jobEvent(t, ctx, pool, "jobs.metrics.unknown")
	if _, err := pool.Exec(ctx, `UPDATE outbox_events SET created_at=clock_timestamp()-interval '5 seconds' WHERE id=$1`, unknown); err != nil {
		t.Fatal(err)
	}
	insertJob(t, ctx, pool, 1)
	metrics := telemetry.NewMetrics(func() float64 { return 0 }, func() float64 { return 0 }, jobs.NewMetricsReader(pool))
	w := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := w.Body.String()
	for _, expected := range []string{"outbox_undispatched_events 1", `job_queue_depth{status="pending"} 1`, "queue_metrics_up 1"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("metrics output missing %q: %s", expected, body)
		}
	}
	if strings.Contains(body, unknown.String()) || strings.Contains(body, "jobs.metrics.unknown") {
		t.Fatalf("metrics leak event identity: %s", body)
	}
}
