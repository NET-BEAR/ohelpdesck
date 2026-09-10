package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"github.com/krassus/ohelpdesck/internal/platform/telemetry"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Check func(context.Context) error
type errorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details"`
	RequestID string            `json:"request_id"`
}

func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": errorBody{code, message, map[string]string{}, w.Header().Get("X-Request-ID")}})
}
func New(checks map[string]Check, origins string, metrics *telemetry.Metrics, loggers ...*slog.Logger) http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		status := 200
		defer func() {
			if metrics != nil {
				method := r.Method
				if method != "GET" && method != "POST" && method != "OPTIONS" {
					method = "OTHER"
				}
				metrics.Requests.WithLabelValues(method, strconv.Itoa(status)).Inc()
				metrics.Duration.Observe(time.Since(start).Seconds())
			}
		}()
		id := r.Header.Get("X-Request-ID")
		valid := len(id) > 0 && len(id) <= 128
		for _, c := range id {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				valid = false
			}
		}
		if !valid {
			b := make([]byte, 16)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		if len(loggers) > 0 {
			defer func() {
				loggers[0].Info("http request completed", "request_id", id, "trace_id", trace.SpanFromContext(r.Context()).SpanContext().TraceID().String(), "status", status)
			}()
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			allowed := false
			for _, o := range strings.Split(origins, ",") {
				if strings.TrimSpace(o) == origin {
					allowed = true
				}
			}
			if !allowed {
				status = 403
				Error(w, r, status, "origin_denied", "Origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
			if r.Method == "OPTIONS" {
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
				w.WriteHeader(204)
				status = 204
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		if _, e := io.Copy(io.Discard, r.Body); e != nil {
			status = 413
			Error(w, r, status, "body_too_large", "Request body exceeds limit")
			return
		}
		if r.Method != "GET" {
			status = 405
			Error(w, r, status, "method_not_allowed", "Method is not allowed")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health/live":
			_, _ = io.WriteString(w, `{"status":"live"}`)
		case "/health/ready":
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			result := map[string]string{}
			state := "ready"
			type checked struct {
				name string
				err  error
			}
			completed := make(chan checked, len(checks))
			for name, check := range checks {
				go func() { completed <- checked{name, check(ctx)} }()
			}
			for range checks {
				c := <-completed
				name := c.name
				result[name] = "ok"
				if c.err != nil {
					result[name] = "degraded"
					if name == "postgres" {
						status = 503
						state = "not_ready"
						result[name] = "unavailable"
					}
				}
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": state, "checks": result})
		default:
			status = 404
			Error(w, r, status, "not_found", "Resource not found")
		}
	})
	return otelhttp.NewHandler(h, "http.server")
}
