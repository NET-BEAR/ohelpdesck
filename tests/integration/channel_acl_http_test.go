package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/httpserver"
	"github.com/google/uuid"
)

func TestOperatorConversationHTTPRequiresChannelMembership(t *testing.T) {
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}

	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','ACL HTTP integration','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	defer cleanupCoreChannel(t, pool, channelID)
	fixture, err := core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{
		ChannelID: channelID, ExternalMessageID: "acl-http-message-" + uuid.NewString(), ExternalThreadID: "acl-http-thread-" + uuid.NewString(),
		Sender: core.ExternalSender{ExternalUserID: "acl-http-customer-" + uuid.NewString()}, Text: "help", ContentType: core.ContentText,
	})
	if err != nil {
		t.Fatal(err)
	}

	repository := auth.NewRepository(pool)
	assignee, err := repository.Create(ctx, auth.CreateUser{Login: "acl-http-assignee-" + uuid.NewString(), Email: "acl-http-assignee-" + uuid.NewString() + "@example.test", Name: "Assignee", Password: "correct horse battery staple", Role: auth.Agent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversations SET assignee_id=$2 WHERE id=$1`, fixture.ConversationID, assignee.ID); err != nil {
		t.Fatal(err)
	}
	operator, err := repository.Create(ctx, auth.CreateUser{Login: "acl-http-" + uuid.NewString(), Email: "acl-http-" + uuid.NewString() + "@example.test", Name: "Operator", Password: "correct horse battery staple", Role: auth.Agent})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, operator.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, assignee.ID)
	})
	session, csrf, err := repository.CreateSession(ctx, operator.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := httpserver.NewApplication(nil, "", nil, auth.NewOperatorHTTPHandler(repository, false, core.NewConversationService(pool)))

	request := func(method, path, body string, withSession, withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if withSession {
			r.AddCookie(&http.Cookie{Name: "ohelpdesck_session", Value: session})
		}
		if withCSRF {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	statusPath := "/api/v1/conversations/" + fixture.ConversationID.String() + "/status"
	priorityPath := "/api/v1/conversations/" + fixture.ConversationID.String() + "/priority"
	statusBody := `{"expected_version":1,"status":"pending"}`
	priorityBody := `{"expected_version":1,"priority":"high"}`

	if response := request(http.MethodPatch, "/api/v1/conversations/not-a-uuid/status", statusBody, true, true); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid conversation id status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, "/api/v1/conversations/"+fixture.ConversationID.String()+"/unknown", statusBody, true, true); response.Code != http.StatusNotFound {
		t.Fatalf("unknown conversation command status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, statusPath, `{"expected_version":0,"status":"pending"}`, true, true); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid expected_version status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, priorityPath, `{"expected_version":1,"priority":"invalid"}`, true, true); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid priority status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, statusPath, `{`, true, true); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed JSON status=%d body=%s", response.Code, response.Body.String())
	}

	if response := request(http.MethodPatch, statusPath, statusBody, false, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, statusPath, statusBody, true, false); response.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status=%d body=%s", response.Code, response.Body.String())
	}
	before, beforeEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if response := request(http.MethodPatch, statusPath, statusBody, true, true); response.Code != http.StatusForbidden {
		t.Fatalf("missing membership status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, priorityPath, priorityBody, true, true); response.Code != http.StatusForbidden {
		t.Fatalf("missing membership priority=%d body=%s", response.Code, response.Body.String())
	}
	after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if !sameConversation(after, before) || afterEvents != beforeEvents {
		t.Fatalf("forbidden mutation before=%+v/%d after=%+v/%d", before, beforeEvents, after, afterEvents)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO channel_memberships(channel_id,user_id,can_read,can_reply) VALUES($1,$2,true,true)`, channelID, operator.ID); err != nil {
		t.Fatal(err)
	}
	if response := request(http.MethodPatch, "/api/v1/conversations/"+uuid.NewString()+"/status", statusBody, true, true); response.Code != http.StatusNotFound {
		t.Fatalf("missing conversation status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, statusPath, `{"expected_version":1,"status":"snoozed"}`, true, true); response.Code != http.StatusBadRequest {
		t.Fatalf("missing snooze deadline status=%d body=%s", response.Code, response.Body.String())
	}
	response := request(http.MethodPatch, statusPath, statusBody, true, true)
	if response.Code != http.StatusOK {
		t.Fatalf("member status=%d body=%s", response.Code, response.Body.String())
	}
	var changed struct {
		Version int64  `json:"version"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &changed); err != nil || changed.Version != 2 || changed.Status != "pending" {
		t.Fatalf("status response=%s changed=%+v err=%v", response.Body.String(), changed, err)
	}
	if response := request(http.MethodPatch, statusPath, `{"expected_version":2,"status":"pending"}`, true, true); response.Code != http.StatusConflict {
		t.Fatalf("invalid transition status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, priorityPath, `{"expected_version":2,"priority":"high"}`, true, true); response.Code != http.StatusOK {
		t.Fatalf("member priority=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, statusPath, `{"expected_version":1,"status":"resolved"}`, true, true); response.Code != http.StatusConflict {
		t.Fatalf("stale version status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_memberships SET can_reply=false WHERE channel_id=$1 AND user_id=$2`, channelID, operator.ID); err != nil {
		t.Fatal(err)
	}
	before, beforeEvents = conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if response := request(http.MethodPatch, statusPath, `{"expected_version":3,"status":"resolved"}`, true, true); response.Code != http.StatusForbidden {
		t.Fatalf("revoked membership status=%d body=%s", response.Code, response.Body.String())
	}
	after, afterEvents = conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if !sameConversation(after, before) || afterEvents != beforeEvents {
		t.Fatalf("revoked membership mutated before=%+v/%d after=%+v/%d", before, beforeEvents, after, afterEvents)
	}
}

func TestOperatorConversationHTTPRejectsUnconfiguredServiceAndSupportsCSRFPreflight(t *testing.T) {
	plain := auth.NewHTTPHandler(nil, false)
	missingService := httptest.NewRecorder()
	plain.ServeHTTP(missingService, httptest.NewRequest(http.MethodPatch, "/api/v1/conversations/00000000-0000-0000-0000-000000000000/status", nil))
	if missingService.Code != http.StatusNotFound {
		t.Fatalf("unconfigured service status=%d", missingService.Code)
	}
	application := httpserver.NewApplication(nil, "https://operator.example.test", nil, plain)
	preflight := httptest.NewRequest(http.MethodOptions, "/api/v1/conversations/00000000-0000-0000-0000-000000000000/status", nil)
	preflight.Header.Set("Origin", "https://operator.example.test")
	response := httptest.NewRecorder()
	application.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Methods") != "GET, POST, PATCH, OPTIONS" || response.Header().Get("Access-Control-Allow-Headers") != "Content-Type, X-Request-ID, X-CSRF-Token" {
		t.Fatalf("preflight status=%d methods=%q headers=%q", response.Code, response.Header().Get("Access-Control-Allow-Methods"), response.Header().Get("Access-Control-Allow-Headers"))
	}
}
