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
	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/httpserver"
	"github.com/google/uuid"
)

func TestChannelAdministrationKeepsCredentialsEncryptedAndOutOfResponses(t *testing.T) {
	requireCoreDatabase(t)
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
	admin, err := repository.Create(ctx, auth.CreateUser{Login: "channel-admin-" + uuid.NewString(), Email: "channel-admin-" + uuid.NewString() + "@example.test", Name: "Channel Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) })
	key := bytes.Repeat([]byte{0x21}, 32)
	cipher, err := channels.NewKeyring("v1", key, nil)
	if err != nil {
		t.Fatal(err)
	}
	session, csrf, err := repository.CreateSession(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	h := httpserver.NewApplication(nil, "", nil, auth.NewOperatorOutboundChannelsHTTPHandler(repository, false, core.NewConversationService(pool), core.NewOutboundService(pool), channels.NewService(pool, cipher)))
	request := func(method, path, body string, authenticated, mutation bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if authenticated {
			r.AddCookie(&http.Cookie{Name: "ohelpdesck_session", Value: session})
		}
		if mutation {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if response := request(http.MethodGet, "/api/v1/channels", "", false, false); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list=%d", response.Code)
	}
	if response := request(http.MethodGet, "/api/v1/channels/not-a-uuid", "", true, false); response.Code != http.StatusBadRequest {
		t.Fatalf("malformed channel id=%d", response.Code)
	}
	const secret = "provider-secret-must-not-return"
	create := request(http.MethodPost, "/api/v1/channels", `{"type":"telegram_bot","name":"Support bot","config":{"webhook_path":"/inbound"},"credentials":{"token":"`+secret+`"}}`, true, true)
	if create.Code != http.StatusCreated || bytes.Contains(create.Body.Bytes(), []byte(secret)) {
		t.Fatalf("unsafe create status=%d body=%s", create.Code, create.Body.String())
	}
	var created channels.Channel
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil || created.ID == "" || !created.HasCredentials || created.Enabled || created.Status != channels.StatusDisabled {
		t.Fatalf("create response=%s channel=%+v err=%v", create.Body.String(), created, err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id=$1`, created.ID) })
	var sealed []byte
	if err := pool.QueryRow(ctx, `SELECT credentials_ciphertext FROM channels WHERE id=$1`, created.ID).Scan(&sealed); err != nil || bytes.Contains(sealed, []byte(secret)) {
		t.Fatalf("database credentials are plaintext or absent: %v", err)
	}
	plain, err := cipher.Open(created.ID, created.Type, channels.CredentialMetadata{KeyID: "v1", Version: 1}, sealed)
	if err != nil || !bytes.Contains(plain, []byte(secret)) {
		t.Fatalf("stored credentials are not decryptable: %v", err)
	}
	var auditAction, auditDetails string
	if err := pool.QueryRow(ctx, `SELECT action,details::text FROM channel_audit_events WHERE channel_id=$1 ORDER BY occurred_at DESC LIMIT 1`, created.ID).Scan(&auditAction, &auditDetails); err != nil || auditAction != "created" || bytes.Contains([]byte(auditDetails), []byte(secret)) {
		t.Fatalf("unsafe or missing audit action=%q details=%q err=%v", auditAction, auditDetails, err)
	}

	if response := request(http.MethodGet, "/api/v1/channels", "", true, false); response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(secret)) {
		t.Fatalf("unsafe list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/channels/"+created.ID, "", true, false); response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte(secret)) {
		t.Fatalf("unsafe get status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPatch, "/api/v1/channels/"+created.ID, `{"name":"Renamed bot","credentials":{"token":"rotated-secret"}}`, true, true); response.Code != http.StatusOK || bytes.Contains(response.Body.Bytes(), []byte("rotated-secret")) {
		t.Fatalf("unsafe patch status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/channels/"+created.ID+"/actions/validate", "", true, true); response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"valid":false`)) {
		t.Fatalf("validation status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/channels/"+created.ID+"/actions/enable", "", true, true); response.Code != http.StatusConflict {
		t.Fatalf("provider-less enable status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/channels/"+created.ID+"/actions/disable", "", true, true); response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"enabled":false`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"disabled"`)) {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}
}
