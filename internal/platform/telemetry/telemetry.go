package telemetry

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	"log/slog"
	"net"
	"net/http"
	"time"
)

func Init(settings ...string) func(context.Context) error {
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) { slog.Warn("telemetry operation failed") }))
	options := []trace.TracerProviderOption{}
	if len(settings) > 0 {
		options = append(options, trace.WithResource(resource.NewWithAttributes("", attribute.String("service.name", settings[0]))))
	}
	if len(settings) > 1 && settings[1] != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(settings[1]), otlptracehttp.WithTimeout(2*time.Second))
		if err == nil {
			options = append(options, trace.WithBatcher(safeExporter{exporter}))
		}
	}
	p := trace.NewTracerProvider(options...)
	otel.SetTracerProvider(p)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return p.Shutdown
}

type Metrics struct {
	Registry *prometheus.Registry
	Requests *prometheus.CounterVec
	Duration prometheus.Histogram
}

func NewMetrics(acquired, idle func() float64) *Metrics {
	r := prometheus.NewRegistry()
	m := &Metrics{r, prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_requests_total", Help: "HTTP requests"}, []string{"method", "status"}), prometheus.NewHistogram(prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP latency"})}
	r.MustRegister(m.Requests, m.Duration, prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "db_pool_acquired_connections", Help: "Acquired connections"}, acquired), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "db_pool_idle_connections", Help: "Idle connections"}, idle), prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "process_start_time_seconds", Help: "Process start"}, func() func() float64 { start := float64(time.Now().Unix()); return func() float64 { return start } }()))
	return m
}
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}

type userAgent struct{ next http.RoundTripper }

func (u userAgent) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header.Set("User-Agent", "ohelpdesck/1")
	return u.next.RoundTrip(c)
}
func HTTPClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second, Transport: userAgent{otelhttp.NewTransport(&http.Transport{Proxy: http.ProxyFromEnvironment, DialContext: (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, MaxIdleConns: 50, MaxIdleConnsPerHost: 10, IdleConnTimeout: 60 * time.Second, ResponseHeaderTimeout: 10 * time.Second})}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("redirects disabled") }}
}
