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
	firstLogin, secondLogin := loginRateLimitKey("Alice"), loginRateLimitKey("Bob")
	if firstLogin == secondLogin || !limiter.Allow(firstLogin) || !limiter.Allow(firstLogin) || limiter.Allow(firstLogin) || !limiter.Allow(secondLogin) {
		t.Fatal("rate limit is not isolated by normalized login")
	}
	limiter.Reset(firstLogin)
	if !limiter.Allow(firstLogin) || limiterKey("invalid") != "invalid" {
		t.Fatal("rate limit reset/key fallback failed")
	}
	if !handler.(*HTTPHandler).secureCookie {
		t.Fatal("secure-cookie policy lost")
	}
}
