package channels

import (
	"bytes"
	"testing"
)

func TestAESGCMCipherRoundTripAndRejectsMalformedCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	cipher, err := NewAESGCMCipher(key)
	if err != nil {
		t.Fatalf("NewAESGCMCipher: %v", err)
	}

	sealed, metadata, err := cipher.Seal("11111111-1111-1111-1111-111111111111", TypeTelegramBot, []byte(`{"token":"never-log-me"}`))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(sealed, []byte("never-log-me")) {
		t.Fatal("ciphertext contains plaintext credential")
	}
	plain, err := cipher.Open("11111111-1111-1111-1111-111111111111", TypeTelegramBot, metadata, sealed)
	if err != nil || string(plain) != `{"token":"never-log-me"}` {
		t.Fatalf("Decrypt = %q, %v", plain, err)
	}
	if _, err := cipher.Open("11111111-1111-1111-1111-111111111111", TypeTelegramBot, metadata, sealed[:cipher.NonceSize()-1]); err == nil {
		t.Fatal("malformed ciphertext accepted")
	}
}

func TestAESGCMCipherRejectsInvalidKeyLength(t *testing.T) {
	if _, err := NewAESGCMCipher([]byte("too-short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestAESGCMCipherBindsCredentialsToChannelIdentityAndMetadata(t *testing.T) {
	cipher, err := NewAESGCMCipher(bytes.Repeat([]byte{0x24}, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, metadata, err := cipher.Seal("11111111-1111-1111-1111-111111111111", TypeTelegramBot, []byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cipher.Open("22222222-2222-2222-2222-222222222222", TypeTelegramBot, metadata, sealed); err == nil {
		t.Fatal("swapped channel blob accepted")
	}
	if _, err := cipher.Open("11111111-1111-1111-1111-111111111111", TypeMAX, metadata, sealed); err == nil {
		t.Fatal("swapped type blob accepted")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := cipher.Open("11111111-1111-1111-1111-111111111111", TypeTelegramBot, metadata, sealed); err == nil {
		t.Fatal("tampered blob accepted")
	}
}

func TestKeyringReadsPreviousKeyButWritesActiveKey(t *testing.T) {
	oldKey := bytes.Repeat([]byte{0x11}, 32)
	newKey := bytes.Repeat([]byte{0x12}, 32)
	oldRing, err := NewKeyring("old", oldKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	sealed, meta, err := oldRing.Seal("11111111-1111-1111-1111-111111111111", TypeVK, []byte(`{"token":"old"}`))
	if err != nil {
		t.Fatal(err)
	}
	newRing, err := NewKeyring("new", newKey, map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := newRing.Open("11111111-1111-1111-1111-111111111111", TypeVK, meta, sealed); err != nil || string(plain) != `{"token":"old"}` {
		t.Fatalf("previous key read %q %v", plain, err)
	}
	_, next, err := newRing.Seal("11111111-1111-1111-1111-111111111111", TypeVK, []byte(`{"token":"new"}`))
	if err != nil || next.KeyID != "new" {
		t.Fatalf("active write metadata=%+v err=%v", next, err)
	}
}
