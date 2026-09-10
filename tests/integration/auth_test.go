package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/httpserver"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/telemetry"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestLocalPasswordLoginAndAuthorization(t *testing.T) {
	required(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	repository := auth.NewRepository(pool)
	name := "auth-test-" + time.Now().UTC().Format("20060102150405.000000000")
	admin, err := repository.Create(ctx, auth.CreateUser{Login: name + "-admin", Email: name + "@example.test", Name: "Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", admin.ID) }()
	agent, err := repository.Create(ctx, auth.CreateUser{Login: name + "-agent", Email: name + "+agent@example.test", Name: "Agent", Password: "correct horse battery staple", Role: auth.Agent})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", agent.ID) }()
	metrics := telemetry.NewMetrics(func() float64 { return 0 }, func() float64 { return 0 })
	var securityLogs bytes.Buffer
	h := httpserver.NewApplication(nil, "", metrics, auth.NewHTTPHandler(repository, false, auth.Observability{Metrics: metrics, Log: slog.New(slog.NewJSONHandler(&securityLogs, nil))}))

	login := func(login, password string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"login": login, "password": password})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
		request.RemoteAddr = "127.0.0.1:12345"
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		return response
	}
	if response := login(admin.Login, "wrong password"); response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status: %d", response.Code)
	}
	response := login(admin.Login, "correct horse battery staple")
	if response.Code != http.StatusOK || response.Result().Cookies()[0].HttpOnly != true {
		t.Fatalf("login failed: %d %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	var loginBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &loginBody); err != nil || loginBody.CSRFToken == "" {
		t.Fatal("missing csrf token")
	}
	me := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	me.AddCookie(cookie)
	meResponse := httptest.NewRecorder()
	h.ServeHTTP(meResponse, me)
	if meResponse.Code != http.StatusOK || bytes.Contains(meResponse.Body.Bytes(), []byte("password_hash")) {
		t.Fatalf("unsafe profile response: %d %s", meResponse.Code, meResponse.Body.String())
	}
	agentLogin := login(agent.Login, "correct horse battery staple")
	agentCookie := agentLogin.Result().Cookies()[0]
	agentBody := struct {
		CSRFToken string `json:"csrf_token"`
	}{}
	_ = json.Unmarshal(agentLogin.Body.Bytes(), &agentBody)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(`{"login":"new","email":"new@example.test","name":"New","password":"correct horse battery staple","role":"agent"}`))
	create.AddCookie(agentCookie)
	create.Header.Set("X-CSRF-Token", agentBody.CSRFToken)
	createResponse := httptest.NewRecorder()
	h.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("agent was allowed to manage users: %d", createResponse.Code)
	}
	metricsResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, metric := range []string{"auth_requests_total", "auth_failures_total", "authorization_denied_total"} {
		if !bytes.Contains(metricsResponse.Body.Bytes(), []byte(metric)) {
			t.Fatalf("missing security metric %s", metric)
		}
	}
	if bytes.Contains(securityLogs.Bytes(), []byte("correct horse battery staple")) || bytes.Contains(securityLogs.Bytes(), []byte(admin.Login)) {
		t.Fatal("security log leaked credentials or login")
	}
	badCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(`{}`))
	badCSRF.AddCookie(cookie)
	badCSRFResponse := httptest.NewRecorder()
	h.ServeHTTP(badCSRFResponse, badCSRF)
	if badCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("mutation without csrf accepted: %d", badCSRFResponse.Code)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if response := login(name+"-rate-target", "wrong password"); response.Code != http.StatusUnauthorized {
			t.Fatalf("rate limit pre-threshold status: %d", response.Code)
		}
	}
	if response := login(name+"-rate-target", "wrong password"); response.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limit threshold status: %d", response.Code)
	}
	if rateLimited := testutil.ToFloat64(metrics.AuthFailures.WithLabelValues("rate_limited")); rateLimited != 1 {
		t.Fatalf("rate limit metric: %f", rateLimited)
	}
	if response := login(agent.Login, "correct horse battery staple"); response.Code != http.StatusOK {
		t.Fatalf("rate limiter affected a different login: %d", response.Code)
	}
}

func TestAdministrationBundlesAndSessionFailures(t *testing.T) {
	required(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	repository := auth.NewRepository(pool)
	prefix := "admin-test-" + time.Now().UTC().Format("20060102150405.000000000")
	admin, err := repository.Create(ctx, auth.CreateUser{Login: prefix + "-admin", Email: prefix + "@example.test", Name: "Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE login LIKE $1", prefix+"%")
		_, _ = pool.Exec(context.Background(), "DELETE FROM permission_bundles WHERE name LIKE $1", prefix+"%")
	}()
	metrics := telemetry.NewMetrics(func() float64 { return 0 }, func() float64 { return 0 })
	var securityLogs bytes.Buffer
	h := httpserver.NewApplication(nil, "", metrics, auth.NewHTTPHandler(repository, false, auth.Observability{Metrics: metrics, Log: slog.New(slog.NewJSONHandler(&securityLogs, nil))}))
	request := func(method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if response := request(http.MethodGet, "/api/v1/users", "", nil, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthed users: %d", response.Code)
	}
	loginBody := fmt.Sprintf(`{"login":%q,"password":"correct horse battery staple"}`, admin.Login)
	login := request(http.MethodPost, "/api/v1/auth/login", loginBody, nil, "")
	if login.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", login.Code, login.Body.String())
	}
	cookie := login.Result().Cookies()[0]
	var session struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.CSRFToken == "" {
		t.Fatal("csrf missing")
	}
	if response := request(http.MethodPost, "/api/v1/permission-bundles", `{"name":"bad","permissions":["role.manage"]}`, cookie, session.CSRFToken); response.Code != http.StatusBadRequest {
		t.Fatalf("dangerous bundle: %d", response.Code)
	}
	bundleResponse := request(http.MethodPost, "/api/v1/permission-bundles", fmt.Sprintf(`{"name":%q,"permissions":["analytics.read"]}`, prefix+"-analytics"), cookie, session.CSRFToken)
	if bundleResponse.Code != http.StatusCreated {
		t.Fatalf("bundle create: %d %s", bundleResponse.Code, bundleResponse.Body.String())
	}
	var bundle auth.PermissionBundle
	if err := json.Unmarshal(bundleResponse.Body.Bytes(), &bundle); err != nil || bundle.ID == "" {
		t.Fatal("bundle response invalid")
	}
	if response := request(http.MethodGet, "/api/v1/permission-bundles", "", cookie, ""); response.Code != http.StatusOK {
		t.Fatalf("bundle list: %d", response.Code)
	}
	createResponse := request(http.MethodPost, "/api/v1/users", fmt.Sprintf(`{"login":%q,"email":%q,"name":"Operator","password":"correct horse battery staple","role":"agent"}`, prefix+"-agent", prefix+"+agent@example.test"), cookie, session.CSRFToken)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("user create: %d %s", createResponse.Code, createResponse.Body.String())
	}
	var agent struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &agent); err != nil || agent.ID == "" {
		t.Fatal("user response invalid")
	}
	if found, err := repository.ByID(ctx, agent.ID); err != nil || found.Login != prefix+"-agent" {
		t.Fatalf("user lookup by id: %v", err)
	}
	if _, err := repository.ByID(ctx, "00000000-0000-0000-0000-000000000002"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("missing user by id: %v", err)
	}
	if _, err := repository.ByLogin(ctx, prefix+"-missing"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("missing user by login: %v", err)
	}
	if users, err := repository.List(ctx); err != nil || len(users) < 2 {
		t.Fatalf("user list: %v", err)
	}
	if response := request(http.MethodPost, "/api/v1/users", fmt.Sprintf(`{"login":%q,"email":%q,"name":"Operator","password":"correct horse battery staple","role":"agent"}`, prefix+"-agent", prefix+"+agent@example.test"), cookie, session.CSRFToken); response.Code != http.StatusConflict {
		t.Fatalf("duplicate user: %d", response.Code)
	}
	concurrentCollision := func(loginA, emailA, loginB, emailB string) {
		bodies := []string{
			fmt.Sprintf(`{"login":%q,"email":%q,"name":"Concurrent","password":"correct horse battery staple","role":"agent"}`, loginA, emailA),
			fmt.Sprintf(`{"login":%q,"email":%q,"name":"Concurrent","password":"correct horse battery staple","role":"agent"}`, loginB, emailB),
		}
		start := make(chan struct{})
		ready := sync.WaitGroup{}
		responses := make(chan int, len(bodies))
		for _, body := range bodies {
			ready.Add(1)
			go func(body string) {
				ready.Done()
				<-start
				responses <- request(http.MethodPost, "/api/v1/users", body, cookie, session.CSRFToken).Code
			}(body)
		}
		ready.Wait()
		close(start)
		created, conflicted := 0, 0
		for range bodies {
			status := <-responses
			if status == http.StatusCreated {
				created++
			}
			if status == http.StatusConflict {
				conflicted++
			}
		}
		if created != 1 || conflicted != 1 {
			t.Fatalf("concurrent duplicate create statuses: created=%d conflict=%d", created, conflicted)
		}
		var persisted int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE lower(login)=lower($1) OR lower(email)=lower($2)", loginA, emailA).Scan(&persisted); err != nil || persisted != 1 {
			t.Fatalf("concurrent duplicate persistence: count=%d err=%v", persisted, err)
		}
	}
	loginCollision := prefix + "-case-login"
	concurrentCollision(loginCollision, prefix+"+login-one@example.test", strings.ToUpper(loginCollision), prefix+"+login-two@example.test")
	emailCollision := prefix + "+case-email@example.test"
	concurrentCollision(prefix+"-email-one", emailCollision, prefix+"-email-two", strings.ToUpper(emailCollision))
	if response := request(http.MethodPost, "/api/v1/users", `{}`, cookie, session.CSRFToken); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid user accepted: %d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/users", "", cookie, ""); response.Code != http.StatusOK {
		t.Fatalf("admin list users: %d", response.Code)
	}
	if response := request(http.MethodPut, "/api/v1/users/"+agent.ID+"/permission-bundles", fmt.Sprintf(`{"bundle_ids":[%q]}`, bundle.ID), cookie, session.CSRFToken); response.Code != http.StatusNoContent {
		t.Fatalf("bundle assignment: %d %s", response.Code, response.Body.String())
	}
	if err := repository.SetUserBundles(ctx, agent.ID, []string{"00000000-0000-0000-0000-000000000001"}); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("unknown bundle assignment: %v", err)
	}
	principal := auth.Principal{UserID: agent.ID, Role: auth.Agent}
	if permissions, err := repository.EffectivePermissions(ctx, principal); err != nil || !slices.Contains(permissions, auth.PermissionAnalyticsRead) {
		t.Fatalf("effective bundle permission: %v %v", permissions, err)
	}
	if err := repository.SetUserBundles(ctx, agent.ID, []string{bundle.ID, bundle.ID}); err != nil {
		t.Fatalf("duplicate bundle assignment: %v", err)
	}
	if response := request(http.MethodPatch, "/api/v1/users/"+agent.ID, fmt.Sprintf(`{"name":"Operator Updated","email":%q,"password":"another correct battery staple","role":"supervisor"}`, prefix+"+updated@example.test"), cookie, session.CSRFToken); response.Code != http.StatusOK {
		t.Fatalf("full user update: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, "/api/v1/users/"+agent.ID, `{"status":"disabled"}`, cookie, session.CSRFToken); response.Code != http.StatusOK {
		t.Fatalf("disable user: %d", response.Code)
	}
	if response := request(http.MethodPatch, "/api/v1/users/00000000-0000-0000-0000-000000000001", `{"name":"Nope"}`, cookie, session.CSRFToken); response.Code != http.StatusNotFound {
		t.Fatalf("unknown user patch: %d", response.Code)
	}
	if response := request(http.MethodPatch, "/api/v1/users/"+agent.ID, `{"role":"invalid"}`, cookie, session.CSRFToken); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid role patch: %d", response.Code)
	}
	if response := request(http.MethodPost, "/api/v1/auth/login", fmt.Sprintf(`{"login":%q,"password":"correct horse battery staple"}`, prefix+"-agent"), nil, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled user login: %d", response.Code)
	}
	updatesBefore := testutil.ToFloat64(metrics.UserAdminChanges.WithLabelValues("update"))
	if response := request(http.MethodPatch, "/api/v1/users/"+admin.ID, `{"status":"disabled"}`, cookie, session.CSRFToken); response.Code != http.StatusConflict {
		t.Fatalf("last admin disabled: %d", response.Code)
	}
	if updatesAfter := testutil.ToFloat64(metrics.UserAdminChanges.WithLabelValues("update")); updatesAfter != updatesBefore {
		t.Fatalf("rejected last-admin update was audited as successful: before=%f after=%f", updatesBefore, updatesAfter)
	}
	if response := request(http.MethodPost, "/api/v1/auth/logout", "", cookie, session.CSRFToken); response.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/me", "", cookie, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d", response.Code)
	}
	metricsResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !bytes.Contains(metricsResponse.Body.Bytes(), []byte("user_admin_changes_total")) {
		t.Fatal("missing user administration metric")
	}
	if bytes.Contains(securityLogs.Bytes(), []byte("correct horse battery staple")) || bytes.Contains(securityLogs.Bytes(), []byte(admin.Login)) {
		t.Fatal("security log leaked credentials or login")
	}
}

func TestConcurrentAdministratorDisableKeepsOneActiveAdministrator(t *testing.T) {
	required(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	repository := auth.NewRepository(pool)
	prefix := "admin-race-" + time.Now().UTC().Format("20060102150405.000000000")
	create := func(suffix string) auth.User {
		user, err := repository.Create(ctx, auth.CreateUser{Login: prefix + suffix, Email: prefix + suffix + "@example.test", Name: "Admin", Password: "correct horse battery staple", Role: auth.Administrator})
		if err != nil {
			t.Fatal(err)
		}
		return user
	}
	first, second := create("-one"), create("-two")
	defer func() { _, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE login LIKE $1", prefix+"%") }()
	var activeBefore int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='administrator' AND status='active'").Scan(&activeBefore); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, id := range []string{first.ID, second.ID} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			<-start
			status := auth.Disabled
			_, err := repository.Update(context.Background(), id, auth.UpdateUser{Status: &status})
			results <- err
		}(id)
	}
	close(start)
	wait.Wait()
	close(results)
	successes, protected := 0, 0
	for err := range results {
		if err == nil {
			successes++
		}
		if errors.Is(err, auth.ErrForbidden) {
			protected++
		}
	}
	expectedSuccesses := min(2, max(0, activeBefore-1))
	if successes != expectedSuccesses || protected != 2-expectedSuccesses {
		t.Fatalf("last-admin guard outcomes: initial_active=%d success=%d protected=%d", activeBefore, successes, protected)
	}
	var activeAfter int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE role='administrator' AND status='active'").Scan(&activeAfter); err != nil || activeAfter < 1 {
		t.Fatalf("active administrators after concurrent update: count=%d err=%v", activeAfter, err)
	}
}
