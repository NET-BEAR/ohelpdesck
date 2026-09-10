package telemetry

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace"
	"net/url"
	"strings"
)

// safeExporter is the single privacy boundary for every span exported by Init.
// Networking uses the original requests; only the telemetry copy is sanitized.
type safeExporter struct{ trace.SpanExporter }

func (e safeExporter) ExportSpans(ctx context.Context, spans []trace.ReadOnlySpan) error {
	sanitized := make([]trace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		sanitized[i] = safeSpan{s}
	}
	return e.SpanExporter.ExportSpans(ctx, sanitized)
}

type safeSpan struct{ trace.ReadOnlySpan }

func (s safeSpan) Attributes() []attribute.KeyValue {
	return safeAttributes(s.ReadOnlySpan.Attributes())
}
func (s safeSpan) Events() []trace.Event {
	original := s.ReadOnlySpan.Events()
	events := make([]trace.Event, len(original))
	copy(events, original)
	for i := range events {
		events[i].Attributes = safeAttributes(events[i].Attributes)
	}
	return events
}
func (s safeSpan) Status() trace.Status {
	status := s.ReadOnlySpan.Status()
	if status.Description != "" {
		status.Description = "operation failed"
	}
	return status
}
func safeAttributes(attrs []attribute.KeyValue) []attribute.KeyValue {
	result := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		key := strings.ToLower(string(a.Key))
		sensitive := key == "enduser.id" || key == "exception.message" || key == "exception.stacktrace" || key == "url.query"
		for _, fragment := range []string{"password", "secret", "token", "authorization", "cookie"} {
			sensitive = sensitive || strings.Contains(key, fragment)
		}
		if sensitive {
			continue
		}
		if key == "url.full" || key == "http.url" || key == "http.target" {
			parsed, err := url.Parse(a.Value.AsString())
			if err != nil {
				continue
			}
			parsed.User = nil
			parsed.RawQuery = ""
			parsed.ForceQuery = false
			parsed.Fragment = ""
			a.Value = attribute.StringValue(parsed.String())
		}
		result = append(result, a)
	}
	return result
}
