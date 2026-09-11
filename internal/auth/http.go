package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/platform/telemetry"
)

const sessionCookie = "ohelpdesck_session"

type HTTPHandler struct {
	repository    *Repository
	secureCookie  bool
	limiter       *loginLimiter
	metrics       *telemetry.Metrics
	log           *slog.Logger
	conversations *core.ConversationService
	outbound      *core.OutboundService
}

type Observability struct {
	Metrics *telemetry.Metrics
	Log     *slog.Logger
}

func NewHTTPHandler(repository *Repository, secureCookie bool, observability ...Observability) http.Handler {
	return newHTTPHandler(repository, secureCookie, nil, observability...)
}

// NewOperatorHTTPHandler adds the provider-neutral Conversation command boundary
// to the existing authenticated API. The core service locks Channel membership
// inside the same transaction as the mutation.
func NewOperatorHTTPHandler(repository *Repository, secureCookie bool, conversations *core.ConversationService, observability ...Observability) http.Handler {
	return newHTTPHandler(repository, secureCookie, conversations, observability...)
}

// NewOperatorOutboundHTTPHandler mounts the authenticated queue boundary in
// addition to operator conversation commands.
func NewOperatorOutboundHTTPHandler(repository *Repository, secureCookie bool, conversations *core.ConversationService, outbound *core.OutboundService, observability ...Observability) http.Handler {
	h := newHTTPHandler(repository, secureCookie, conversations, observability...)
	h.outbound = outbound
	return h
}

func newHTTPHandler(repository *Repository, secureCookie bool, conversations *core.ConversationService, observability ...Observability) *HTTPHandler {
	h := &HTTPHandler{repository: repository, secureCookie: secureCookie, limiter: newLoginLimiter(5, 15*time.Minute), conversations: conversations}
	if len(observability) > 0 {
		h.metrics, h.log = observability[0].Metrics, observability[0].Log
	}
	return h
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/api/v1/auth/login" && r.Method == http.MethodPost:
		h.login(w, r)
	case r.URL.Path == "/api/v1/auth/logout" && r.Method == http.MethodPost:
		principal, _, ok := h.authenticate(w, r, true)
		if !ok {
			return
		}
		_ = principal
		cookie, _ := r.Cookie(sessionCookie)
		if err := h.repository.RevokeSession(r.Context(), cookie.Value); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		h.clearCookie(w)
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/api/v1/conversations/") && r.Method == http.MethodPatch:
		h.conversationCommand(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/conversations/") && r.Method == http.MethodPost:
		h.queueOutbound(w, r)
	case r.URL.Path == "/api/v1/me" && r.Method == http.MethodGet:
		principal, _, ok := h.authenticate(w, r, false)
		if !ok {
			return
		}
		permissions, err := h.repository.EffectivePermissions(r.Context(), principal)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		writeJSON(w, http.StatusOK, safeUser(principal, Active, permissions))
	case r.URL.Path == "/api/v1/users" && r.Method == http.MethodGet:
		principal, _, ok := h.authenticate(w, r, false)
		if !ok || !h.allowed(w, r, principal, PermissionUserManage) {
			return
		}
		users, err := h.repository.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		result := make([]safeUserResponse, 0, len(users))
		for _, user := range users {
			result = append(result, safeUser(Principal{UserID: user.ID, Login: user.Login, Email: user.Email, Name: user.Name, Role: user.Role}, user.Status, nil))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": result})
	case r.URL.Path == "/api/v1/users" && r.Method == http.MethodPost:
		principal, _, ok := h.authenticate(w, r, true)
		if !ok || !h.allowed(w, r, principal, PermissionUserManage) {
			return
		}
		var input CreateUser
		if !decodeJSON(w, r, &input) {
			return
		}
		user, err := h.repository.Create(r.Context(), input)
		if errors.Is(err, ErrConflict) {
			writeError(w, http.StatusConflict, "user_already_exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		h.userAdminChange("create", principal.UserID, user.ID)
		writeJSON(w, http.StatusCreated, safeUser(Principal{UserID: user.ID, Login: user.Login, Email: user.Email, Name: user.Name, Role: user.Role}, user.Status, nil))
	case r.URL.Path == "/api/v1/permission-bundles" && r.Method == http.MethodGet:
		principal, _, ok := h.authenticate(w, r, false)
		if !ok || !h.allowed(w, r, principal, PermissionRoleManage) {
			return
		}
		bundles, err := h.repository.ListBundles(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": bundles})
	case r.URL.Path == "/api/v1/permission-bundles" && r.Method == http.MethodPost:
		principal, _, ok := h.authenticate(w, r, true)
		if !ok || !h.allowed(w, r, principal, PermissionRoleManage) {
			return
		}
		var input struct {
			Name        string       `json:"name"`
			Permissions []Permission `json:"permissions"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		bundle, err := h.repository.CreateBundle(r.Context(), input.Name, input.Permissions)
		if errors.Is(err, ErrConflict) {
			writeError(w, http.StatusConflict, "bundle_already_exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		writeJSON(w, http.StatusCreated, bundle)
	case strings.HasPrefix(r.URL.Path, "/api/v1/users/") && strings.HasSuffix(r.URL.Path, "/permission-bundles") && r.Method == http.MethodPut:
		principal, _, ok := h.authenticate(w, r, true)
		if !ok || !h.allowed(w, r, principal, PermissionRoleManage) {
			return
		}
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/users/"), "/permission-bundles")
		if id == "" || strings.Contains(id, "/") {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		var input struct {
			BundleIDs []string `json:"bundle_ids"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		if err := h.repository.SetUserBundles(r.Context(), id, input.BundleIDs); errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found")
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(r.URL.Path, "/api/v1/users/") && r.Method == http.MethodPatch:
		principal, _, ok := h.authenticate(w, r, true)
		if !ok || !h.allowed(w, r, principal, PermissionUserManage) {
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/users/")
		if id == "" || strings.Contains(id, "/") {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		var input UpdateUser
		if !decodeJSON(w, r, &input) {
			return
		}
		user, err := h.repository.Update(r.Context(), id, input)
		if errors.Is(err, ErrNotFound) {
			writeError(w, http.StatusNotFound, "user_not_found")
			return
		}
		if errors.Is(err, ErrForbidden) {
			writeError(w, http.StatusConflict, "last_administrator")
			return
		}
		if errors.Is(err, ErrConflict) {
			writeError(w, http.StatusConflict, "user_already_exists")
			return
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "validation_failed")
			return
		}
		h.userAdminChange("update", principal.UserID, user.ID)
		writeJSON(w, http.StatusOK, safeUser(Principal{UserID: user.ID, Login: user.Login, Email: user.Email, Name: user.Name, Role: user.Role}, user.Status, nil))
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *HTTPHandler) login(w http.ResponseWriter, r *http.Request) {
	var input struct{ Login, Password string }
	if !decodeJSON(w, r, &input) {
		return
	}
	key := loginRateLimitKey(input.Login)
	if !h.limiter.Allow(key) {
		h.authFailure("rate_limited")
		writeError(w, http.StatusTooManyRequests, "login_rate_limited")
		return
	}
	user, err := h.repository.ByLogin(r.Context(), input.Login)
	hash := dummyPasswordHash()
	validUser := err == nil && user.Status == Active
	if err == nil {
		hash = user.PasswordHash
	}
	validPassword := VerifyPassword(hash, input.Password)
	if !validUser || !validPassword {
		h.authFailure("invalid_credentials")
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	token, csrf, err := h.repository.CreateSession(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	h.limiter.Reset(key)
	h.authRequest("success")
	h.setCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]string{"csrf_token": csrf})
}

func (h *HTTPHandler) authenticate(w http.ResponseWriter, r *http.Request, mutation bool) (Principal, string, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		h.authFailure("session_invalid")
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return Principal{}, "", false
	}
	principal, csrfHash, err := h.repository.PrincipalBySession(r.Context(), cookie.Value)
	if err != nil {
		h.authFailure("session_invalid")
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return Principal{}, "", false
	}
	if mutation {
		provided := r.Header.Get("X-CSRF-Token")
		if provided == "" || subtle.ConstantTimeCompare([]byte(csrfHash), []byte(HashSecret(provided))) != 1 {
			h.authFailure("csrf_invalid")
			writeError(w, http.StatusForbidden, "csrf_invalid")
			return Principal{}, "", false
		}
	}
	return principal, csrfHash, true
}

func (h *HTTPHandler) allowed(w http.ResponseWriter, r *http.Request, principal Principal, permission Permission) bool {
	permissions, err := h.repository.EffectivePermissions(r.Context(), principal)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error")
		return false
	}
	for _, granted := range permissions {
		if granted == permission {
			return true
		}
	}
	if !Allowed(principal.Role, permission) {
		if h.metrics != nil {
			h.metrics.AuthorizationDenied.WithLabelValues(string(permission)).Inc()
		}
		if h.log != nil {
			h.log.Warn("security authorization denied", "event", "authorization_denied", "actor_id", principal.UserID, "permission", permission)
		}
		writeError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}

func (h *HTTPHandler) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: int((12 * time.Hour).Seconds())})
}
func (h *HTTPHandler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

type safeUserResponse struct {
	ID          string       `json:"id"`
	Login       string       `json:"login"`
	Email       string       `json:"email"`
	Name        string       `json:"name"`
	Role        Role         `json:"role"`
	Status      Status       `json:"status"`
	Permissions []Permission `json:"permissions,omitempty"`
}

func safeUser(p Principal, status Status, permissions []Permission) safeUserResponse {
	return safeUserResponse{ID: p.UserID, Login: p.Login, Email: p.Email, Name: p.Name, Role: p.Role, Status: status, Permissions: permissions}
}
func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "validation_failed")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "validation_failed")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": "Request could not be completed", "details": map[string]string{}, "request_id": w.Header().Get("X-Request-ID")}})
}

type loginLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	attempt map[string]loginAttempt
}
type loginAttempt struct {
	count int
	until time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{max: max, window: window, attempt: map[string]loginAttempt{}}
}
func (l *loginLimiter) Allow(key string) bool {
	key = limiterKey(key)
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.attempt[key]
	now := time.Now()
	if entry.until.Before(now) {
		entry = loginAttempt{until: now.Add(l.window)}
	}
	if entry.count >= l.max {
		return false
	}
	entry.count++
	l.attempt[key] = entry
	return true
}
func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempt, limiterKey(key))
}
func limiterKey(remoteAddress string) string {
	return remoteAddress
}

func loginRateLimitKey(login string) string {
	value := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(login))))
	return string(value[:])
}

var cachedDummyPasswordHash string
var dummyPasswordOnce sync.Once

func dummyPasswordHash() string {
	dummyPasswordOnce.Do(func() { cachedDummyPasswordHash, _ = HashPassword("local-auth-dummy-password") })
	return cachedDummyPasswordHash
}

func (h *HTTPHandler) authRequest(result string) {
	if h.metrics != nil {
		h.metrics.AuthRequests.WithLabelValues(result).Inc()
	}
}
func (h *HTTPHandler) authFailure(reason string) {
	if h.metrics != nil {
		h.metrics.AuthRequests.WithLabelValues("failure").Inc()
		h.metrics.AuthFailures.WithLabelValues(reason).Inc()
	}
	if h.log != nil {
		h.log.Warn("security authentication failed", "event", "authentication_failed", "reason", reason)
	}
}
func (h *HTTPHandler) userAdminChange(operation, actorID, targetID string) {
	if h.metrics != nil {
		h.metrics.UserAdminChanges.WithLabelValues(operation).Inc()
	}
	if h.log != nil {
		h.log.Info("security user administration changed", "event", "user_administration_changed", "operation", operation, "actor_id", actorID, "target_id", targetID)
	}
}
