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

type channelEdgeValidator struct{ err error }

func (v channelEdgeValidator) Validate(context.Context, channels.Channel) error { return v.err }

func TestChannelCredentialRotationReadsPriorKeyAndRejectsRetiredWriter(t *testing.T) {
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
	admin, err := repository.Create(ctx, auth.CreateUser{Login: "channel-edge-" + uuid.NewString(), Email: "channel-edge-" + uuid.NewString() + "@example.test", Name: "Rotation Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) })
	audit := channels.AuditContext{ActorID: admin.ID, CorrelationID: uuid.NewString()}

	oldKey := bytes.Repeat([]byte{0x51}, 32)
	newKey := bytes.Repeat([]byte{0x52}, 32)
	oldRing, err := channels.NewKeyring("old", oldKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	oldRegistry := channels.NewRegistry()
	oldRegistry.Register(channels.TypeTelegramBot, channelEdgeValidator{})
	oldService := channels.NewServiceWithRegistry(pool, oldRing, oldRegistry)
	created, err := oldService.Create(ctx, audit, channels.CreateInput{Type: channels.TypeTelegramBot, Name: "Rotation bot", Config: json.RawMessage(`{"webhook_path":"/rotation"}`), Credentials: json.RawMessage(`{"token":"rotation-secret"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id=$1`, created.ID) })

	newRing, err := channels.NewKeyring("new", newKey, map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	newRegistry := channels.NewRegistry()
	newRegistry.Register(channels.TypeTelegramBot, channelEdgeValidator{})
	newService := channels.NewServiceWithRegistry(pool, newRing, newRegistry)
	before, err := newService.Credentials(ctx, created.ID)
	if err != nil || !bytes.Contains(before, []byte("rotation-secret")) {
		t.Fatalf("prior key read credentials=%q err=%v", before, err)
	}
	if _, err := newService.ReencryptCredentials(ctx, audit, created.ID); err != nil {
		t.Fatalf("reencrypt with active key: %v", err)
	}
	var keyID string
	if err := pool.QueryRow(ctx, `SELECT credentials_key_id FROM channels WHERE id=$1`, created.ID).Scan(&keyID); err != nil || keyID != "new" {
		t.Fatalf("credentials key id=%q err=%v", keyID, err)
	}
	after, err := newService.Credentials(ctx, created.ID)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("active key read credentials=%q err=%v", after, err)
	}
	if _, err := oldService.Credentials(ctx, created.ID); err == nil {
		t.Fatal("retired writer accepted credentials encrypted with new key")
	}
	if _, err := channels.NewKeyring("new", newKey, map[string][]byte{"new": oldKey}); err == nil {
		t.Fatal("duplicate active key id accepted")
	}
}

func TestChannelServiceRejectsInvalidPatchAndPreservesStaleConfig(t *testing.T) {
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
	admin, err := repository.Create(ctx, auth.CreateUser{Login: "channel-edge-stale-" + uuid.NewString(), Email: "channel-edge-stale-" + uuid.NewString() + "@example.test", Name: "Stale Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) })
	keyring, err := channels.NewKeyring("edge", bytes.Repeat([]byte{0x53}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := channels.NewRegistry()
	providerFailure := errors.New("provider rejected credentials")
	registry.Register(channels.TypeTelegramBot, channelEdgeValidator{err: providerFailure})
	service := channels.NewServiceWithRegistry(pool, keyring, registry)
	audit := channels.AuditContext{ActorID: admin.ID, CorrelationID: uuid.NewString()}
	created, err := service.Create(ctx, audit, channels.CreateInput{Type: channels.TypeTelegramBot, Name: "Stale bot", Config: json.RawMessage(`{"webhook_path":"/stale"}`), Credentials: json.RawMessage(`{"token":"stale-secret"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id=$1`, created.ID) })

	blank := " \t"
	invalidConfig := json.RawMessage(`[]`)
	invalidCredentials := json.RawMessage(`null`)
	for _, input := range []channels.PatchInput{{Name: &blank}, {Config: &invalidConfig}, {Credentials: &invalidCredentials}} {
		if _, err := service.Patch(ctx, audit, created.ID, input); !errors.Is(err, channels.ErrInvalid) {
			t.Fatalf("invalid patch %+v error=%v", input, err)
		}
	}
	validation, err := service.Validate(ctx, audit, created.ID)
	if err != nil || validation.Valid {
		t.Fatalf("provider rejection validation=%+v err=%v", validation, err)
	}
	if _, err := service.Enable(ctx, audit, created.ID); !errors.Is(err, providerFailure) {
		t.Fatalf("provider rejection enable error=%v", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE channels SET config_version=config_version+1 WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	name := "stale write"
	if _, err := channels.NewRepository(pool).Patch(ctx, audit, created.ID, 1, channels.PatchInput{Name: &name}, nil, channels.CredentialMetadata{}); !errors.Is(err, channels.ErrStaleConfig) {
		t.Fatalf("stale patch error=%v", err)
	}
	if _, err := channels.NewRepository(pool).SetEnabledIfVersion(ctx, audit, created.ID, 1, true, "enabled"); !errors.Is(err, channels.ErrStaleConfig) {
		t.Fatalf("stale enable error=%v", err)
	}
	if _, err := service.Credentials(ctx, uuid.NewString()); !errors.Is(err, channels.ErrNotFound) {
		t.Fatalf("missing credentials error=%v", err)
	}
}
