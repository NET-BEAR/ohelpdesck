package integration

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/outbox"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type outboundFailingAppender struct{}

func (outboundFailingAppender) Append(context.Context, pgx.Tx, ...outbox.Event) error {
	return errors.New("injected outbound outbox failure")
}

func outboundFixture(t *testing.T) (context.Context, *database.Pool, uuid.UUID, core.ReceiveResult, uuid.UUID) {
	t.Helper()
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Outbound integration','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCoreChannel(t, pool, channelID) })
	inbound, err := core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{ChannelID: channelID, ExternalMessageID: "outbound-inbound-" + uuid.NewString(), ExternalThreadID: "outbound-thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "outbound-customer-" + uuid.NewString()}, Text: "help", ContentType: core.ContentText})
	if err != nil {
		t.Fatal(err)
	}
	user, err := auth.NewRepository(pool).Create(ctx, auth.CreateUser{Login: "outbound-agent-" + uuid.NewString(), Email: "outbound-agent-" + uuid.NewString() + "@example.test", Name: "Outbound agent", Password: "correct horse battery staple", Role: auth.Agent})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, user.ID) })
	userID, err := uuid.Parse(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO channel_memberships(channel_id,user_id,can_read,can_reply) VALUES($1,$2,true,true)`, channelID, userID); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, channelID, inbound, userID
}

func TestQueueOutboundIdempotencyAndMembership(t *testing.T) {
	ctx, pool, channelID, inbound, userID := outboundFixture(t)
	service := core.NewOutboundService(pool)
	command := core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "We are looking into it", IdempotencyKey: "reply-" + uuid.NewString()}
	first, err := service.QueueOutbound(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == uuid.Nil || first.Status != core.MessageQueued || first.Duplicate {
		t.Fatalf("first=%+v", first)
	}
	second, err := service.QueueOutbound(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || second.ID != first.ID || second.Status != core.MessageQueued {
		t.Fatalf("second=%+v first=%+v", second, first)
	}
	var messages, keys, queuedEvents int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE conversation_id=$1 AND direction='outgoing'), (SELECT count(*) FROM message_idempotency_keys WHERE user_id=$2 AND idempotency_key=$3), (SELECT count(*) FROM outbox_events WHERE aggregate_id=$4 AND event_type='message.queued')`, inbound.ConversationID, userID, command.IdempotencyKey, first.ID).Scan(&messages, &keys, &queuedEvents); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || keys != 1 || queuedEvents != 1 {
		t.Fatalf("messages=%d keys=%d events=%d", messages, keys, queuedEvents)
	}
	changed := command
	changed.Text = "different"
	if _, err := service.QueueOutbound(ctx, changed); !errors.Is(err, core.ErrIdempotencyConflict) {
		t.Fatalf("error=%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_memberships SET can_reply=false WHERE channel_id=$1 AND user_id=$2`, channelID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "new", IdempotencyKey: "new-" + uuid.NewString()}); !errors.Is(err, core.ErrConversationForbidden) {
		t.Fatalf("error=%v", err)
	}
}

func TestQueueOutboundSameKeyRaceCreatesOneMessage(t *testing.T) {
	ctx, pool, _, inbound, userID := outboundFixture(t)
	service := core.NewOutboundService(pool)
	command := core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "race", IdempotencyKey: "race-" + uuid.NewString()}
	start := make(chan struct{})
	results := make([]core.Message, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; results[i], errs[i] = service.QueueOutbound(ctx, command) }(i)
	}
	close(start)
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || results[0].ID != results[1].ID || results[0].Duplicate == results[1].Duplicate {
		t.Fatalf("results=%+v errors=%v", results, errs)
	}
	var messages, keys int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE conversation_id=$1 AND direction='outgoing'), (SELECT count(*) FROM message_idempotency_keys WHERE user_id=$2 AND idempotency_key=$3)`, inbound.ConversationID, userID, command.IdempotencyKey).Scan(&messages, &keys); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || keys != 1 {
		t.Fatalf("messages=%d keys=%d", messages, keys)
	}
}

func TestOutboundSentFailedLifecycleRestoresOnlyCoveredEpisode(t *testing.T) {
	ctx, pool, _, inbound, userID := outboundFixture(t)
	service := core.NewOutboundService(pool)
	var originalWaiting time.Time
	if err := pool.QueryRow(ctx, `SELECT waiting_since FROM conversations WHERE id=$1`, inbound.ConversationID).Scan(&originalWaiting); err != nil {
		t.Fatal(err)
	}
	queued, err := service.QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "first", IdempotencyKey: "first-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSent(ctx, core.MarkMessageSentCommand{MessageID: queued.ID, ExternalMessageID: "provider-" + uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	var status core.MessageStatus
	var waiting, firstResponse, closedSince *time.Time
	var closedBy *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT m.status,c.waiting_since,c.first_response_at,c.waiting_closed_by_message_id,c.waiting_closed_since FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id=$1`, queued.ID).Scan(&status, &waiting, &firstResponse, &closedBy, &closedSince); err != nil {
		t.Fatal(err)
	}
	if status != core.MessageSent || waiting != nil || firstResponse == nil || closedBy == nil || *closedBy != queued.ID || closedSince == nil || !closedSince.Equal(originalWaiting) {
		t.Fatalf("status=%s waiting=%v first=%v closedBy=%v closedSince=%v original=%v", status, waiting, firstResponse, closedBy, closedSince, originalWaiting)
	}
	if err := service.MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: queued.ID, ErrorCode: "provider_rejected", ErrorMessage: "rejected"}); err != nil {
		t.Fatal(err)
	}
	var restored bool
	if err := pool.QueryRow(ctx, `SELECT waiting_since IS NOT NULL AND waiting_since=$2 FROM conversations WHERE id=$1`, inbound.ConversationID, originalWaiting).Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("covered episode was not restored")
	}
	var eventType string
	var eventRestored bool
	if err := pool.QueryRow(ctx, `SELECT event_type,(payload->>'waiting_restored')::boolean FROM outbox_events WHERE aggregate_id=$1 ORDER BY created_at DESC LIMIT 1`, queued.ID).Scan(&eventType, &eventRestored); err != nil {
		t.Fatal(err)
	}
	if eventType != "message.delivery_corrected" || !eventRestored {
		t.Fatalf("event=%q restored=%v", eventType, eventRestored)
	}

	second, err := service.QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "second", IdempotencyKey: "second-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkSent(ctx, core.MarkMessageSentCommand{MessageID: second.ID}); err != nil {
		t.Fatal(err)
	}
	var thread, externalUser string
	if err := pool.QueryRow(ctx, `SELECT c.external_thread_id,ci.external_user_id FROM conversations c JOIN contact_identities ci ON ci.id=c.contact_identity_id WHERE c.id=$1`, inbound.ConversationID).Scan(&thread, &externalUser); err != nil {
		t.Fatal(err)
	}
	if _, err := core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{ChannelID: second.ChannelID, ExternalMessageID: "newer-" + uuid.NewString(), ExternalThreadID: thread, Sender: core.ExternalSender{ExternalUserID: externalUser}, Text: "new episode", ContentType: core.ContentText}); err != nil {
		t.Fatal(err)
	}
	if err := service.MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: second.ID, ErrorCode: "ignored", ErrorMessage: "late"}); err != nil {
		t.Fatal(err)
	}
	var newerWaiting *time.Time
	if err := pool.QueryRow(ctx, `SELECT waiting_since FROM conversations WHERE id=$1`, inbound.ConversationID).Scan(&newerWaiting); err != nil {
		t.Fatal(err)
	}
	if newerWaiting == nil {
		t.Fatal("newer waiting episode was overwritten")
	}
	if err := pool.QueryRow(ctx, `SELECT (payload->>'waiting_restored')::boolean FROM outbox_events WHERE aggregate_id=$1 ORDER BY created_at DESC LIMIT 1`, second.ID).Scan(&eventRestored); err != nil {
		t.Fatal(err)
	}
	if eventRestored {
		t.Fatal("late correction restored a newer episode")
	}
}

func TestOutboundRejectsInvalidTransitionAndRollsBackOutboxFailure(t *testing.T) {
	ctx, pool, _, inbound, userID := outboundFixture(t)
	queued, err := core.NewOutboundService(pool).QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "rollback", IdempotencyKey: "rollback-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := core.NewOutboundService(pool).MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: queued.ID, ErrorCode: "x", ErrorMessage: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := core.NewOutboundService(pool).MarkSent(ctx, core.MarkMessageSentCommand{MessageID: queued.ID}); !errors.Is(err, core.ErrInvalidMessageTransition) {
		t.Fatalf("error=%v", err)
	}
	failedService := core.NewOutboundService(pool, outboundFailingAppender{})
	_, err = failedService.QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "no persist", IdempotencyKey: "rollback-outbox-" + uuid.NewString()})
	if err == nil || err.Error() != "injected outbound outbox failure" {
		t.Fatalf("error=%v", err)
	}
}

func TestQueueOutboundHTTPRequiresCSRFAndReturnsCanonicalRetry(t *testing.T) {
	ctx, pool, _, inbound, userID := outboundFixture(t)
	repository := auth.NewRepository(pool)
	session, csrf, err := repository.CreateSession(ctx, userID.String())
	if err != nil {
		t.Fatal(err)
	}
	h := auth.NewOperatorOutboundHTTPHandler(repository, false, core.NewConversationService(pool), core.NewOutboundService(pool))
	request := func(body, key string, csrfHeader bool) int {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/conversations/"+inbound.ConversationID.String()+"/messages", bytes.NewBufferString(body))
		r.AddCookie(&http.Cookie{Name: "ohelpdesck_session", Value: session})
		r.Header.Set("Idempotency-Key", key)
		if csrfHeader {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	key := "http-" + uuid.NewString()
	if got := request(`{"text":"hello"}`, key, false); got != http.StatusForbidden {
		t.Fatalf("missing csrf=%d", got)
	}
	if got := request(`{"text":"hello"}`, key, true); got != http.StatusCreated {
		t.Fatalf("create=%d", got)
	}
	if got := request(`{"text":"hello"}`, key, true); got != http.StatusOK {
		t.Fatalf("retry=%d", got)
	}
	if got := request(`{"text":"different"}`, key, true); got != http.StatusConflict {
		t.Fatalf("conflict=%d", got)
	}
}

func TestOutboundValidationFailureAndQueuedFailurePreserveWaiting(t *testing.T) {
	ctx, pool, _, inbound, userID := outboundFixture(t)
	service := core.NewOutboundService(pool)
	for _, command := range []core.QueueOutboundCommand{
		{},
		{ConversationID: inbound.ConversationID, ActorID: userID, Text: "", HTML: "", IdempotencyKey: "empty"},
		{ConversationID: inbound.ConversationID, ActorID: userID, Text: "body", IdempotencyKey: " "},
		{ConversationID: uuid.New(), ActorID: userID, Text: "body", IdempotencyKey: "missing-" + uuid.NewString()},
		{ConversationID: inbound.ConversationID, ActorID: userID, Text: "body", ReplyToMessageID: ptrUUID(uuid.New()), IdempotencyKey: "reply-" + uuid.NewString()},
	} {
		if _, err := service.QueueOutbound(ctx, command); !errors.Is(err, core.ErrInvalidOutbound) && !errors.Is(err, core.ErrConversationNotFound) {
			t.Fatalf("command=%+v error=%v", command, err)
		}
	}
	queued, err := service.QueueOutbound(ctx, core.QueueOutboundCommand{ConversationID: inbound.ConversationID, ActorID: userID, Text: "will fail", IdempotencyKey: "failed-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: queued.ID, ErrorCode: "temporary", ErrorMessage: "provider unavailable"}); err != nil {
		t.Fatal(err)
	}
	var status core.MessageStatus
	var waiting *time.Time
	var eventType string
	if err := pool.QueryRow(ctx, `SELECT m.status,c.waiting_since,(SELECT event_type FROM outbox_events WHERE aggregate_id=m.id ORDER BY created_at DESC LIMIT 1) FROM messages m JOIN conversations c ON c.id=m.conversation_id WHERE m.id=$1`, queued.ID).Scan(&status, &waiting, &eventType); err != nil {
		t.Fatal(err)
	}
	if status != core.MessageFailed || waiting == nil || eventType != "message.failed" {
		t.Fatalf("status=%s waiting=%v event=%q", status, waiting, eventType)
	}
	if err := service.MarkSent(ctx, core.MarkMessageSentCommand{MessageID: queued.ID}); !errors.Is(err, core.ErrInvalidMessageTransition) {
		t.Fatalf("sent failed message error=%v", err)
	}
	if err := service.MarkSent(ctx, core.MarkMessageSentCommand{MessageID: uuid.New()}); !errors.Is(err, core.ErrMessageNotFound) {
		t.Fatalf("missing sent error=%v", err)
	}
	if err := service.MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: uuid.New(), ErrorCode: "missing"}); !errors.Is(err, core.ErrMessageNotFound) {
		t.Fatalf("missing failed error=%v", err)
	}
	if err := service.MarkFailed(ctx, core.MarkMessageFailedCommand{MessageID: queued.ID}); !errors.Is(err, core.ErrInvalidOutbound) {
		t.Fatalf("blank failure code error=%v", err)
	}
}

func TestQueueOutboundHTTPValidationAndAuthorizationFailures(t *testing.T) {
	ctx, pool, channelID, inbound, userID := outboundFixture(t)
	repository := auth.NewRepository(pool)
	session, csrf, err := repository.CreateSession(ctx, userID.String())
	if err != nil {
		t.Fatal(err)
	}
	h := auth.NewOperatorOutboundHTTPHandler(repository, false, core.NewConversationService(pool), core.NewOutboundService(pool))
	request := func(path, body, key string, sessionCookie, csrfHeader bool) int {
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		if sessionCookie {
			r.AddCookie(&http.Cookie{Name: "ohelpdesck_session", Value: session})
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		if csrfHeader {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	path := "/api/v1/conversations/" + inbound.ConversationID.String() + "/messages"
	if got := request(path, `{"text":"body"}`, "key", false, false); got != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", got)
	}
	if got := request("/api/v1/conversations/not-a-uuid/messages", `{"text":"body"}`, "key", true, true); got != http.StatusBadRequest {
		t.Fatalf("invalid id=%d", got)
	}
	if got := request(path, `{"text":"body"}`, "", true, true); got != http.StatusBadRequest {
		t.Fatalf("missing key=%d", got)
	}
	if got := request(path, `{"text":" "}`, "empty-"+uuid.NewString(), true, true); got != http.StatusBadRequest {
		t.Fatalf("empty content=%d", got)
	}
	if got := request("/api/v1/conversations/"+uuid.NewString()+"/messages", `{"text":"body"}`, "missing-"+uuid.NewString(), true, true); got != http.StatusNotFound {
		t.Fatalf("missing conversation=%d", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE channel_memberships SET can_reply=false WHERE channel_id=$1 AND user_id=$2`, channelID, userID); err != nil {
		t.Fatal(err)
	}
	if got := request(path, `{"text":"body"}`, "forbidden-"+uuid.NewString(), true, true); got != http.StatusForbidden {
		t.Fatalf("missing membership=%d", got)
	}
	missing := auth.NewOperatorHTTPHandler(repository, false, core.NewConversationService(pool))
	w := httptest.NewRecorder()
	missing.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unconfigured outbound=%d", w.Code)
	}
}

func ptrUUID(value uuid.UUID) *uuid.UUID { return &value }
