package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPInputGuardsAndRateLimiter(t *testing.T) {
	handler := NewHTTPHandler(nil, true)
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/v1/unknown", nil))
	if unknown.Code != http.StatusNotFound || !strings.Contains(unknown.Body.String(), "not_found") {
		t.Fatal("unknown route not normalized")
	}
	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
	invalidRequest.RemoteAddr = "127.0.0.2:1"
	handler.ServeHTTP(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON accepted: %d", invalid.Code)
	}
	trailing := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/anything", bytes.NewBufferString(`{} {}`))
	if decodeJSON(trailing, request, &map[string]any{}) {
		t.Fatal("trailing JSON accepted")
	}
	limiter := newLoginLimiter(2, time.Minute)
	if !limiter.Allow("127.0.0.1:1") || !limiter.Allow("127.0.0.1:2") || limiter.Allow("127.0.0.1:3") {
		t.Fatal("rate limit did not use remote host")
	}
	limiter.Reset("127.0.0.1:4")
	if !limiter.Allow("127.0.0.1:5") || limiterKey("invalid") != "invalid" {
		t.Fatal("rate limit reset/key fallback failed")
	}
	if !handler.(*HTTPHandler).secureCookie {
		t.Fatal("secure-cookie policy lost")
	}
	for attempt := 0; attempt < 6; attempt++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader("{"))
		request.RemoteAddr = "127.0.0.3:1234"
		handler.ServeHTTP(response, request)
		if attempt < 5 && response.Code != http.StatusBadRequest {
			t.Fatalf("unexpected pre-limit status %d", response.Code)
		}
		if attempt == 5 && response.Code != http.StatusTooManyRequests {
			t.Fatalf("missing login rate limit: %d", response.Code)
		}
	}
}
