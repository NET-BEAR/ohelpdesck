package logging

import (
	"io"
	"log/slog"
	"strings"
)

func New(w io.Writer, service, environment string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		switch a.Key {
		case slog.TimeKey:
			a.Key = "timestamp"
		case slog.MessageKey:
			a.Key = "message"
		}
		k := strings.ToLower(a.Key)
		for _, s := range []string{"password", "secret", "token", "authorization", "cookie", "database_url", "redis_url", "access_key"} {
			if strings.Contains(k, s) {
				a.Value = slog.StringValue("[REDACTED]")
				return a
			}
		}
		if a.Value.Kind() == slog.KindAny {
			a.Value = slog.StringValue("[REDACTED]")
		}
		return a
	}})).With("service", service, "environment", environment, "request_id", "", "trace_id", "")
}
