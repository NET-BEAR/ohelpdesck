package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
)

type channelValidationStub struct{ err error }

func (s channelValidationStub) Validate(context.Context, channels.Channel) error { return s.err }

func TestChannelServiceLifecycleAndCredentialRotation(t *testing.T) {
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

	repository := auth.NewRepository(pool)
	admin, err := repository.Create(ctx, auth.CreateUser{Login: "channel-coverage-" + uuid.NewString(), Email: "channel-coverage-" + uuid.NewString() + "@example.test", Name: "Coverage Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) })

	keyring, err := channels.NewKeyring("coverage-v1", bytes.Repeat([]byte{0x41}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := channels.NewRegistry()
	registry.Register(channels.TypeTelegramBot, channelValidationStub{})
	service := channels.NewServiceWithRegistry(pool, keyring, registry)
	audit := channels.AuditContext{ActorID: admin.ID, CorrelationID: uuid.NewString()}

	if _, err := service.Create(ctx, audit, channels.CreateInput{Type: channels.Type("unknown"), Name: "Invalid", Config: json.RawMessage(`{}`), Credentials: json.RawMessage(`{}`)}); !errors.Is(err, channels.ErrInvalid) {
		t.Fatalf("invalid channel type error=%v", err)
	}
	created, err := service.Create(ctx, audit, channels.CreateInput{Type: channels.TypeTelegramBot, Name: "Coverage bot", Config: json.RawMessage(`{"webhook_path":"/coverage"}`), Credentials: json.RawMessage(`{"token":"coverage-secret-v1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_audit_events WHERE channel_id=$1`, created.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id=$1`, created.ID)
	})
	if created.Enabled || created.Status != channels.StatusDisabled || !created.HasCredentials {
		t.Fatalf("unexpected created channel=%+v", created)
	}

	listed, err := service.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !containsChannel(listed, created.ID) {
		t.Fatalf("created channel not listed: %+v", listed)
	}
	if _, err := service.Get(ctx, uuid.NewString()); !errors.Is(err, channels.ErrNotFound) {
		t.Fatalf("missing get error=%v", err)
	}
	if _, err := service.Patch(ctx, audit, created.ID, channels.PatchInput{}); !errors.Is(err, channels.ErrInvalid) {
		t.Fatalf("empty patch error=%v", err)
	}

	name := "Coverage bot renamed"
	patched, err := service.Patch(ctx, audit, created.ID, channels.PatchInput{Name: &name})
	if err != nil || patched.Name != name {
		t.Fatalf("name patch channel=%+v err=%v", patched, err)
	}
	config := json.RawMessage(`{"webhook_path":"/coverage-v2"}`)
	patched, err = service.Patch(ctx, audit, created.ID, channels.PatchInput{Config: &config})
	if err != nil || !bytes.Contains(patched.Config, []byte("coverage-v2")) {
		t.Fatalf("config patch channel=%+v err=%v", patched, err)
	}
	credentials := json.RawMessage(`{"token":"coverage-secret-v2"}`)
	patched, err = service.Patch(ctx, audit, created.ID, channels.PatchInput{Credentials: &credentials})
	if err != nil || !patched.HasCredentials {
		t.Fatalf("credential patch channel=%+v err=%v", patched, err)
	}

	validation, err := service.Validate(ctx, audit, created.ID)
	if err != nil || !validation.Valid {
		t.Fatalf("validation result=%+v err=%v", validation, err)
	}
	enabled, err := service.Enable(ctx, audit, created.ID)
	if err != nil || !enabled.Enabled || enabled.Status != channels.StatusActive {
		t.Fatalf("enable channel=%+v err=%v", enabled, err)
	}
	disabled, err := service.Disable(ctx, audit, created.ID)
	if err != nil || disabled.Enabled || disabled.Status != channels.StatusDisabled {
		t.Fatalf("disable channel=%+v err=%v", disabled, err)
	}

	rotated, err := service.ReencryptCredentials(ctx, audit, created.ID)
	if err != nil || !rotated.HasCredentials {
		t.Fatalf("reencrypt channel=%+v err=%v", rotated, err)
	}

	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM channel_audit_events WHERE channel_id=$1`, created.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits < 7 {
		t.Fatalf("audits=%d", audits)
	}
}

func TestChannelServiceReportsProviderValidationAndTypeBoundCredentials(t *testing.T) {
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
	admin, err := repository.Create(ctx, auth.CreateUser{Login: "channel-coverage-provider-" + uuid.NewString(), Email: "channel-coverage-provider-" + uuid.NewString() + "@example.test", Name: "Coverage Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) })
	keyring, err := channels.NewKeyring("coverage-v1", bytes.Repeat([]byte{0x42}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := channels.NewService(pool, keyring)
	audit := channels.AuditContext{ActorID: admin.ID, CorrelationID: uuid.NewString()}
	created, err := service.Create(ctx, audit, channels.CreateInput{Type: channels.TypeEmail, Name: "Coverage email", Config: json.RawMessage(`{}`), Credentials: json.RawMessage(`{"password":"coverage"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_audit_events WHERE channel_id=$1`, created.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id=$1`, created.ID)
	})
	if _, err := service.Enable(ctx, audit, created.ID); !errors.Is(err, channels.ErrProviderValidationUnavailable) {
		t.Fatalf("provider unavailable enable error=%v", err)
	}
}

func containsChannel(items []channels.Channel, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
