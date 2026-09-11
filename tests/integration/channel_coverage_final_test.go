package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
)

type channelFinalValidator struct{ err error }

func (v channelFinalValidator) Validate(context.Context, channels.Channel) error { return v.err }

func TestChannelCryptoAndRegistryFailClosedAtTrustBoundaries(t *testing.T) {
	key := bytes.Repeat([]byte{0x61}, 32)
	cipher, err := channels.NewAESGCMCipherWithKeyID("final", key)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		keyID string
		key   []byte
	}{{keyID: "", key: key}, {keyID: "short", key: key[:31]}} {
		if _, err := channels.NewAESGCMCipherWithKeyID(input.keyID, input.key); err == nil {
			t.Fatalf("invalid cipher setup accepted key_id=%q len=%d", input.keyID, len(input.key))
		}
	}
	if _, err := channels.NewKeyring("active", key, map[string][]byte{"prior": key[:31]}); err == nil {
		t.Fatal("keyring accepted an invalid prior rotation key")
	}
	sealed, metadata, err := cipher.Seal("11111111-1111-1111-1111-111111111111", channels.TypeTelegramBot, []byte(`{"token":"final-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidMetadata := []channels.CredentialMetadata{
		{KeyID: "other", Version: metadata.Version},
		{KeyID: metadata.KeyID, Version: metadata.Version + 1},
	}
	for _, candidate := range invalidMetadata {
		if _, err := cipher.Open("11111111-1111-1111-1111-111111111111", channels.TypeTelegramBot, candidate, sealed); err == nil {
			t.Fatalf("mismatched metadata accepted: %+v", candidate)
		}
	}
	if _, err := cipher.Open("other-channel", channels.TypeTelegramBot, metadata, sealed); err == nil {
		t.Fatal("ciphertext opened under a different channel AAD")
	}
	var nilCipher *channels.AESGCMCipher
	if _, _, err := nilCipher.Seal("channel", channels.TypeEmail, []byte(`{}`)); err == nil {
		t.Fatal("nil cipher sealed credentials")
	}
	if _, err := nilCipher.Open("channel", channels.TypeEmail, metadata, sealed); err == nil {
		t.Fatal("nil cipher opened credentials")
	}
	var nilRing *channels.Keyring
	if _, _, err := nilRing.Seal("channel", channels.TypeEmail, []byte(`{}`)); err == nil {
		t.Fatal("nil keyring sealed credentials")
	}
	if _, err := nilRing.Open("channel", channels.TypeEmail, metadata, sealed); err == nil {
		t.Fatal("nil keyring opened credentials")
	}

	registry := channels.NewRegistry()
	cases := []channels.Channel{
		{Type: channels.TypeTelegramBot, Config: json.RawMessage(`{"webhook_path":"/hook"}`)},
		{Type: channels.Type("attacker_defined"), Config: json.RawMessage(`{}`), HasCredentials: true},
		{Type: channels.TypeTelegramBot, Config: json.RawMessage(`{`), HasCredentials: true},
		{Type: channels.TypeTelegramUser, Config: json.RawMessage(`{}`), HasCredentials: true},
		{Type: channels.TypeTelegramUser, Config: json.RawMessage(`{"api_id":""}`), HasCredentials: true},
	}
	for _, channel := range cases {
		if err := registry.ValidateForEnable(context.Background(), channel); err == nil {
			t.Fatalf("unsafe registry validation accepted channel=%+v", channel)
		}
	}
	registry.Register(channels.TypeTelegramBot, channelFinalValidator{})
	if err := registry.ValidateForEnable(context.Background(), channels.Channel{Type: channels.TypeTelegramBot, Config: json.RawMessage(`{"webhook_path":"/hook"}`), HasCredentials: true}); err != nil {
		t.Fatalf("registered provider validation error=%v", err)
	}
	registry.Register(channels.Type("attacker_defined"), channelFinalValidator{})
	registry.Register(channels.TypeTelegramBot, nil)
	if got := channels.Types(); !slices.IsSortedFunc(got, func(left, right channels.Type) int {
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	}) || !slices.Contains(got, channels.TypeTelegramBot) {
		t.Fatalf("types are not sorted/complete: %v", got)
	}
}

func TestChannelServiceRejectsStoredCredentialTampering(t *testing.T) {
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
	admin, err := repository.Create(ctx, auth.CreateUser{Login: "channel-final-" + uuid.NewString(), Email: "channel-final-" + uuid.NewString() + "@example.test", Name: "Crypto Admin", Password: "correct horse battery staple", Role: auth.Administrator})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, admin.ID) })
	keyring, err := channels.NewKeyring("final", bytes.Repeat([]byte{0x62}, 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := channels.NewRegistry()
	registry.Register(channels.TypeTelegramBot, channelFinalValidator{})
	service := channels.NewServiceWithRegistry(pool, keyring, registry)
	audit := channels.AuditContext{ActorID: admin.ID, CorrelationID: uuid.NewString()}
	created, err := service.Create(ctx, audit, channels.CreateInput{Type: channels.TypeTelegramBot, Name: "Tamper bot", Config: json.RawMessage(`{"webhook_path":"/tamper"}`), Credentials: json.RawMessage(`{"token":"tamper-secret"}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM channel_audit_events WHERE channel_id=$1`, created.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM channels WHERE id=$1`, created.ID)
	})
	if plain, err := service.Credentials(ctx, created.ID); err != nil || !bytes.Contains(plain, []byte("tamper-secret")) {
		t.Fatalf("baseline credentials=%q err=%v", plain, err)
	}

	for _, update := range []struct {
		query string
		args  []any
	}{
		{`UPDATE channels SET credentials_ciphertext=$2 WHERE id=$1`, []any{created.ID, []byte("not-a-valid-sealed-credential")}},
		{`UPDATE channels SET credentials_key_id='unknown', credentials_version=1 WHERE id=$1`, []any{created.ID}},
		{`UPDATE channels SET credentials_key_id='final', credentials_version=2 WHERE id=$1`, []any{created.ID}},
	} {
		if _, err := pool.Exec(ctx, update.query, update.args...); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Credentials(ctx, created.ID); err == nil {
			t.Fatalf("tampered credential accepted query=%q", update.query)
		}
	}
	if _, err := channels.NewService(pool, nil).Create(ctx, audit, channels.CreateInput{Type: channels.TypeEmail, Name: "No key", Config: json.RawMessage(`{}`), Credentials: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("service without keyring created channel")
	}
	if _, err := service.ReencryptCredentials(ctx, audit, created.ID); err == nil {
		t.Fatal("reencrypt accepted invalid stored metadata")
	}
	if _, err := service.Credentials(ctx, uuid.NewString()); !errors.Is(err, channels.ErrNotFound) {
		t.Fatalf("missing credential error=%v", err)
	}
	missing := uuid.NewString()
	name := "missing channel"
	if _, err := service.Patch(ctx, audit, missing, channels.PatchInput{Name: &name}); !errors.Is(err, channels.ErrNotFound) {
		t.Fatalf("missing patch error=%v", err)
	}
	if _, err := service.Enable(ctx, audit, missing); !errors.Is(err, channels.ErrNotFound) {
		t.Fatalf("missing enable error=%v", err)
	}
}
