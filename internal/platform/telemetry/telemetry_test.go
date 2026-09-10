package telemetry

import (
	"context"
	"go.opentelemetry.io/otel"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTelemetry(t *testing.T) {
	stop := Init()
	if e := stop(context.Background()); e != nil {
		t.Fatal(e)
	}
	m := NewMetrics(func() float64 { return 2 }, func() float64 { return 3 })
	m.Requests.WithLabelValues("GET", "200").Inc()
	m.Duration.Observe(.1)
	w := httptest.NewRecorder()
	m.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	for _, s := range []string{"http_requests_total", "http_request_duration_seconds", "process_start_time_seconds", "db_pool_acquired_connections", "db_pool_idle_connections"} {
		if !strings.Contains(w.Body.String(), s) {
			t.Fatal(s)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "ohelpdesck/1" {
			t.Error("agent")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/", 302)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	client := HTTPClient()
	resp, e := client.Get(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	_ = resp.Body.Close()
	resp, e = client.Get(server.URL + "/redirect")
	if e == nil {
		t.Fatal("redirect allowed")
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}
func TestUnavailableExporter(t *testing.T) {
	stop := Init("test", "http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, span := otel.Tracer("test").Start(ctx, "operation")
	span.End()
	_ = stop(ctx)
}
