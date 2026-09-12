package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthAndSecurity(t *testing.T) {
	for _, tc := range []struct {
		path, method, origin, body string
		pgFail                     bool
		want                       int
	}{{"/health/live", "GET", "", "", true, 200}, {"/health/ready", "GET", "", "", true, 503}, {"/health/ready", "GET", "", "", false, 200}, {"/missing", "GET", "", "", false, 404}, {"/health/live", "POST", "", "", false, 405}, {"/health/live", "GET", "https://bad", "", false, 403}, {"/health/live", "OPTIONS", "https://ok", "", false, 204}, {"/health/live", "GET", "https://ok", "", false, 200}, {"/health/live", "GET", "", strings.Repeat("x", (1<<20)+1), false, 413}} {
		t.Run(tc.path+tc.method+tc.origin, func(t *testing.T) {
			fail := func(context.Context) error { return errors.New("secret-dsn") }
			pg := func(context.Context) error {
				if tc.pgFail {
					return fail(nil)
				}
				return nil
			}
			h := New(map[string]Check{"postgres": pg, "redis": fail, "object_storage": fail}, "https://ok", nil)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("X-Request-ID", "invalid id")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
			if len(w.Header().Get("X-Request-ID")) != 32 || strings.Contains(w.Body.String(), "secret-dsn") {
				t.Fatal("unsafe response")
			}
			if w.Code >= 400 && tc.path != "/health/ready" {
				var b map[string]errorBody
				if json.Unmarshal(w.Body.Bytes(), &b) != nil || b["error"].RequestID == "" {
					t.Fatal("invalid error")
				}
			}
		})
	}
	r := httptest.NewRequest("GET", "/health/live", nil)
	r.Header.Set("X-Request-ID", "request_123")
	w := httptest.NewRecorder()
	New(nil, "", nil).ServeHTTP(w, r)
	if w.Header().Get("X-Request-ID") != "request_123" {
		t.Fatal("id changed")
	}
}
func TestReadinessDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	h := New(map[string]Check{"postgres": func(context.Context) error { return nil }, "redis": func(context.Context) error { <-release; return nil }}, "", nil)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(w, httptest.NewRequest("GET", "/health/ready", nil)); close(done) }()
	select {
	case <-done:
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"redis":"degraded"`) {
			t.Fatalf("unexpected %d %s", w.Code, w.Body.String())
		}
	case <-time.After(2200 * time.Millisecond):
		t.Fatal("readiness exceeded deadline")
	}
}
