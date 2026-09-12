package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/workspace"
	"github.com/google/uuid"
)

// TestWorkspaceHTTPReadAndAssignment exercises the complete operator read path
// against PostgreSQL: session boundary, membership ACL, stable list cursor,
// safe detail/timeline and the versioned assignment transaction.
func TestWorkspaceHTTPReadAndAssignment(t *testing.T) {
	ctx, pool, channelID, inbound, agentID := outboundFixture(t)
	repo := auth.NewRepository(pool)
	target, err := repo.Create(ctx, auth.CreateUser{Login: "workspace-target-" + uuid.NewString(), Email: "workspace-target-" + uuid.NewString() + "@example.test", Name: "Workspace Target", Password: "correct horse battery staple", Role: auth.Agent})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, target.ID) })
	targetID := uuid.MustParse(target.ID)
	operator, err := repo.Create(ctx, auth.CreateUser{Login: "workspace-supervisor-" + uuid.NewString(), Email: "workspace-supervisor-" + uuid.NewString() + "@example.test", Name: "Workspace Supervisor", Password: "correct horse battery staple", Role: auth.Supervisor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, operator.ID) })
	operatorID := uuid.MustParse(operator.ID)
	if _, err := pool.Exec(ctx, `INSERT INTO channel_memberships(channel_id,user_id,can_read,can_reply,can_reassign) VALUES($1,$2,true,true,true)`, channelID, operatorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO channel_memberships(channel_id,user_id,can_read,can_reply) VALUES($1,$2,true,true)`, channelID, targetID); err != nil {
		t.Fatal(err)
	}
	// A second thread makes the page cursor meaningful while keeping the actor in
	// exactly one allowed channel.
	if _, err := core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{ChannelID: channelID, ExternalMessageID: "workspace-second-" + uuid.NewString(), ExternalThreadID: "workspace-thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "workspace-customer-" + uuid.NewString()}, Text: "second conversation", ContentType: core.ContentText}); err != nil {
		t.Fatal(err)
	}
	queued, err := core.NewOutboundService(pool).QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: agentID, Text: "queued response", IdempotencyKey: "workspace-" + uuid.NewString()})
	if err != nil || queued.Status != core.MessageQueued {
		t.Fatalf("queue=%+v err=%v", queued, err)
	}
	// Service-level guards are still authoritative when a caller bypasses HTTP.
	readerOnly, err := repo.Create(ctx, auth.CreateUser{Login: "workspace-reader-" + uuid.NewString(), Email: "workspace-reader-" + uuid.NewString() + "@example.test", Name: "Workspace Reader", Password: "correct horse battery staple", Role: auth.Supervisor})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, readerOnly.ID) })
	readerOnlyID := uuid.MustParse(readerOnly.ID)
	if _, err := pool.Exec(ctx, `INSERT INTO channel_memberships(channel_id,user_id,can_read,can_reply,can_reassign) VALUES($1,$2,true,true,false)`, channelID, readerOnlyID); err != nil {
		t.Fatal(err)
	}
	if _, err := core.NewConversationService(pool).Assign(ctx, core.AssignConversation{ConversationID: inbound.ConversationID, ExpectedVersion: 1, ActorID: readerOnlyID}); !errors.Is(err, core.ErrConversationForbidden) {
		t.Fatalf("reader-only assignment error=%v", err)
	}
	reads := workspace.NewService(pool)
	if _, err := reads.List(ctx, operatorID, workspace.ListQuery{Limit: -1}); !errors.Is(err, workspace.ErrInvalidQuery) {
		t.Fatalf("invalid list=%v", err)
	}
	if _, err := reads.Messages(ctx, operatorID, inbound.ConversationID, "bad", "", 1); !errors.Is(err, workspace.ErrInvalidQuery) {
		t.Fatalf("invalid timeline cursor=%v", err)
	}
	if _, err := reads.Messages(ctx, operatorID, inbound.ConversationID, "x", "y", 1); !errors.Is(err, workspace.ErrInvalidQuery) {
		t.Fatalf("conflicting timeline cursors=%v", err)
	}
	if _, err := reads.Messages(ctx, operatorID, inbound.ConversationID, "", "", 101); !errors.Is(err, workspace.ErrInvalidQuery) {
		t.Fatalf("oversized timeline limit=%v", err)
	}
	if _, err := reads.Detail(ctx, operatorID, uuid.Nil, true, true); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("nil detail id=%v", err)
	}

	session, csrf, err := repo.CreateSession(ctx, operatorID.String())
	if err != nil {
		t.Fatal(err)
	}
	h := auth.WithWorkspace(auth.NewOperatorOutboundHTTPHandler(repo, false, core.NewConversationService(pool), core.NewOutboundService(pool)), workspace.NewService(pool))
	for _, path := range []string{"/api/v1/conversations", "/api/v1/conversations/" + inbound.ConversationID.String()} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s=%d", path, w.Code)
		}
	}
	if _, err := core.NewConversationService(pool).Assign(ctx, core.AssignConversation{ConversationID: uuid.New(), ExpectedVersion: 1, ActorID: operatorID}); !errors.Is(err, core.ErrConversationNotFound) {
		t.Fatalf("missing assignment=%v", err)
	}
	if _, err := core.NewConversationService(pool).Assign(ctx, core.AssignConversation{ConversationID: inbound.ConversationID}); !errors.Is(err, core.ErrConversationNotFound) {
		t.Fatalf("invalid assignment=%v", err)
	}
	request := func(method, path, body string, csrfHeader bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "ohelpdesck_session", Value: session})
		if csrfHeader {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	list := request(http.MethodGet, "/api/v1/conversations?limit=1&sort=number_desc", "", false)
	if list.Code != http.StatusOK {
		t.Fatalf("list=%d body=%s", list.Code, list.Body.String())
	}
	var page struct {
		Items []struct {
			ID      uuid.UUID `json:"id"`
			Number  int64     `json:"number"`
			Channel struct {
				ID uuid.UUID `json:"id"`
			} `json:"channel"`
			Contact struct {
				ID          uuid.UUID `json:"id"`
				DisplayName string    `json:"display_name"`
			} `json:"contact"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.NextCursor == nil || page.Items[0].Channel.ID != channelID || page.Items[0].Contact.ID == uuid.Nil {
		t.Fatalf("page=%s err=%v", list.Body.String(), err)
	}
	second := request(http.MethodGet, "/api/v1/conversations?limit=1&cursor="+*page.NextCursor, "", false)
	if second.Code != http.StatusOK || bytes.Contains(second.Body.Bytes(), []byte(page.Items[0].ID.String())) {
		t.Fatalf("second=%d body=%s", second.Code, second.Body.String())
	}

	detail := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String(), "", false)
	if detail.Code != http.StatusOK || bytes.Contains(detail.Body.Bytes(), []byte("external_thread_id")) || !bytes.Contains(detail.Body.Bytes(), []byte(`"can_reassign":true`)) {
		t.Fatalf("detail=%d body=%s", detail.Code, detail.Body.String())
	}
	timeline := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?limit=10", "", false)
	if timeline.Code != http.StatusOK || !bytes.Contains(timeline.Body.Bytes(), []byte(queued.ID.String())) || !bytes.Contains(timeline.Body.Bytes(), []byte(`"status":"queued"`)) {
		t.Fatalf("timeline=%d body=%s", timeline.Code, timeline.Body.String())
	}

	// Filter and cursor validation remain a server concern even when requests are
	// crafted outside the UI.
	for _, path := range []string{
		"/api/v1/conversations?status=invalid",
		"/api/v1/conversations?priority=invalid",
		"/api/v1/conversations?limit=101",
		"/api/v1/conversations?cursor=invalid",
		"/api/v1/conversations?assignee=not-a-uuid",
	} {
		if response := request(http.MethodGet, path, "", false); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid query %s=%d", path, response.Code)
		}
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?before=bad&after=also-bad", "", false); response.Code != http.StatusBadRequest {
		t.Fatalf("combined cursors=%d", response.Code)
	}
	// A non-member active agent is ineligible: server validation must leave the
	// conversation unchanged before an assignment event is appended.
	ineligible, err := repo.Create(ctx, auth.CreateUser{Login: "workspace-ineligible-" + uuid.NewString(), Email: "workspace-ineligible-" + uuid.NewString() + "@example.test", Name: "Workspace Ineligible", Password: "correct horse battery staple", Role: auth.Agent})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, ineligible.ID) })
	if response := request(http.MethodPatch, "/api/v1/conversations/"+inbound.ConversationID.String()+"/assignee", `{"expected_version":1,"assignee_id":"`+ineligible.ID+`"}`, true); response.Code != http.StatusBadRequest {
		t.Fatalf("ineligible=%d body=%s", response.Code, response.Body.String())
	}

	// Cursor modes cover initial/latest, older and newer timeline traversal. The
	// client treats both cursor fields as opaque values.
	for i := 0; i < 2; i++ {
		if _, err := core.NewOutboundService(pool).QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: agentID, Text: "timeline page", IdempotencyKey: "workspace-page-" + uuid.NewString()}); err != nil {
			t.Fatal(err)
		}
	}
	failed, err := core.NewOutboundService(pool).QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: agentID, Text: "definitive provider failure", IdempotencyKey: "workspace-failed-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.NewOutboundService(pool).MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: failed.ID, ErrorCode: "provider_rejected", ErrorMessage: "provider body must not reach workspace"}); err != nil {
		t.Fatal(err)
	}
	firstTimeline := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?limit=1", "", false)
	var timelinePage struct {
		Items []struct {
			ID uuid.UUID `json:"id"`
		} `json:"items"`
		PreviousCursor *string `json:"previous_cursor"`
		NextCursor     *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(firstTimeline.Body.Bytes(), &timelinePage); err != nil || len(timelinePage.Items) != 1 {
		t.Fatalf("timeline page=%s err=%v", firstTimeline.Body.String(), err)
	}
	if timelinePage.NextCursor != nil {
		if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?after="+*timelinePage.NextCursor+"&limit=1", "", false); response.Code != http.StatusOK {
			_, directErr := workspace.NewService(pool).Messages(ctx, operatorID, inbound.ConversationID, "", *timelinePage.NextCursor, 1)
			t.Fatalf("after=%d body=%s direct=%v", response.Code, response.Body.String(), directErr)
		}
		if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?before="+*timelinePage.NextCursor+"&limit=1", "", false); response.Code != http.StatusOK {
			t.Fatalf("before=%d body=%s", response.Code, response.Body.String())
		}
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+uuid.NewString(), "", false); response.Code != http.StatusNotFound {
		t.Fatalf("missing detail=%d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+uuid.NewString()+"/messages", "", false); response.Code != http.StatusNotFound {
		t.Fatalf("missing timeline=%d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/conversations/not-a-uuid", "", false); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid detail=%d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?limit=bad", "", false); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid timeline limit=%d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/unsupported", "", false); response.Code != http.StatusNotFound {
		t.Fatalf("unsupported resource=%d", response.Code)
	}

	if response := request(http.MethodGet, "/api/v1/conversations?channel_id="+channelID.String()+"&status=open&priority=normal&assignee=unassigned", "", false); response.Code != http.StatusOK {
		t.Fatalf("filtered unassigned=%d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/conversations?assignee=me", "", false); response.Code != http.StatusOK {
		t.Fatalf("filtered me=%d", response.Code)
	}
	if response := request(http.MethodPatch, "/api/v1/conversations/"+inbound.ConversationID.String()+"/assignee", `{"expected_version":1,"assignee_id":null}`, true); response.Code != http.StatusOK {
		t.Fatalf("unassign=%d body=%s", response.Code, response.Body.String())
	}

	assign := request(http.MethodPatch, "/api/v1/conversations/"+inbound.ConversationID.String()+"/assignee", `{"expected_version":2,"assignee_id":"`+target.ID+`"}`, true)
	if assign.Code != http.StatusOK {
		t.Fatalf("assign=%d body=%s", assign.Code, assign.Body.String())
	}
	var version int64
	var assignee uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT version,assignee_id FROM conversations WHERE id=$1`, inbound.ConversationID).Scan(&version, &assignee); err != nil || version != 3 || assignee != targetID {
		t.Fatalf("assignment version=%d assignee=%s err=%v", version, assignee, err)
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String(), "", false); response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(target.ID)) {
		t.Fatalf("assigned detail=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages?limit=20", "", false); response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"provider_rejected"`)) || bytes.Contains(response.Body.Bytes(), []byte("provider body must not reach workspace")) {
		t.Fatalf("failed timeline=%d body=%s", response.Code, response.Body.String())
	}
	stale := request(http.MethodPatch, "/api/v1/conversations/"+inbound.ConversationID.String()+"/assignee", `{"expected_version":1,"assignee_id":null}`, true)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale=%d body=%s", stale.Code, stale.Body.String())
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_memberships SET can_read=false WHERE channel_id=$1 AND user_id=$2`, channelID, operatorID); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/conversations/" + inbound.ConversationID.String(), "/api/v1/conversations/" + inbound.ConversationID.String() + "/messages"} {
		if response := request(http.MethodGet, path, "", false); response.Code != http.StatusForbidden || bytes.Contains(response.Body.Bytes(), []byte(inbound.ConversationID.String())) {
			t.Fatalf("denied %s code=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if response := request(http.MethodGet, "/api/v1/conversations", "", false); response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"items":[]`)) {
		t.Fatalf("membership-filtered list=%d body=%s", response.Code, response.Body.String())
	}
}
