package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
func TestSafeTelemetry(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	stop := Init("test")
	otel.Handle(errors.New("provider-secret"))
	_ = stop(context.Background())
	if strings.Contains(logs.String(), "provider-secret") {
		t.Fatal("OTel error leaks")
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(safeExporter{exporter}))
	defer provider.Shutdown(context.Background())
	ctx, span := provider.Tracer("test").Start(context.Background(), "outbound")
	_ = ctx
	span.SetAttributes(attribute.String("url.full", "https://example.com/p?token=url-secret"), attribute.String("enduser.id", "identity-secret"))
	span.RecordError(errors.New("event-secret"))
	span.SetStatus(codes.Error, "status-secret")
	span.End()
	data, _ := json.Marshal(exporter.GetSpans())
	for _, secret := range []string{"url-secret", "identity-secret", "event-secret", "status-secret"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("span leaks " + secret)
		}
	}
}
func TestHTTPTracingPrivacyPreservesRequest(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := trace.NewTracerProvider(trace.WithSyncer(safeExporter{exporter}))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)
	defer provider.Shutdown(context.Background())
	var received atomic.Bool
	server := httptest.NewServer(otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		received.Store(r.URL.Query().Get("token") == "query-sentinel" && ok && user == "identity-sentinel")
		w.WriteHeader(204)
	}), "inbound"))
	defer server.Close()
	target := strings.Replace(server.URL, "http://", "http://identity-sentinel:password-sentinel@", 1) + "/?token=query-sentinel"
	resp, e := HTTPClient().Get(target)
	if e != nil {
		t.Fatal("network call failed")
	}
	_ = resp.Body.Close()
	if !received.Load() {
		t.Fatal("sanitizer changed actual request")
	}
	spans := exporter.GetSpans()
	if len(spans) < 2 {
		t.Fatal("tracing disabled")
	}
	data, _ := json.Marshal(spans)
	for _, secret := range []string{"query-sentinel", "identity-sentinel", "password-sentinel"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("HTTP span leaks " + secret)
		}
	}
}
