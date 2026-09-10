package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/httpserver"
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
	h := httpserver.NewApplication(nil, "", nil, auth.NewHTTPHandler(repository, false))

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
	badCSRF := httptest.NewRequest(http.MethodPost, "/api/v1/users", bytes.NewBufferString(`{}`))
	badCSRF.AddCookie(cookie)
	badCSRFResponse := httptest.NewRecorder()
	h.ServeHTTP(badCSRFResponse, badCSRF)
	if badCSRFResponse.Code != http.StatusForbidden {
		t.Fatalf("mutation without csrf accepted: %d", badCSRFResponse.Code)
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
	h := httpserver.NewApplication(nil, "", nil, auth.NewHTTPHandler(repository, false))
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
	if response := request(http.MethodPatch, "/api/v1/users/"+admin.ID, `{"status":"disabled"}`, cookie, session.CSRFToken); response.Code != http.StatusConflict {
		t.Fatalf("last admin disabled: %d", response.Code)
	}
	if response := request(http.MethodPost, "/api/v1/auth/logout", "", cookie, session.CSRFToken); response.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/me", "", cookie, ""); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session accepted: %d", response.Code)
	}
}
