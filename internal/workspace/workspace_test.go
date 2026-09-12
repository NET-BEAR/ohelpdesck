package workspace

import (
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestListCursorRoundTripAndValidation(t *testing.T) {
	id := uuid.New()
	raw := encodeCursor(listCursor{V: 1, Number: 42, ID: id})
	got, err := decodeCursor(raw)
	if err != nil || got.Number != 42 || got.ID != id {
		t.Fatalf("cursor round trip failed: %#v %v", got, err)
	}
	for _, bad := range []string{"not-a-cursor", encodeCursor(listCursor{V: 2, Number: 42, ID: id}), encodeCursor(listCursor{V: 1, Number: 0, ID: id})} {
		if _, err := decodeCursor(bad); err == nil {
			t.Fatalf("invalid cursor accepted: %q", bad)
		}
	}
}
func TestMessageCursorRoundTripAndValidation(t *testing.T) {
	id := uuid.New()
	raw := encodeMessageCursor(messageCursor{V: 1, CreatedAt: time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC), ID: id})
	got, err := decodeMessageCursor(raw)
	if err != nil || got.ID != id {
		t.Fatalf("message cursor round trip failed: %#v %v", got, err)
	}
}

func TestCursorsRejectOversizedAndWrongVersion(t *testing.T) {
	if _, err := decodeCursor(string(make([]byte, 513))); err == nil {
		t.Fatal("oversized list cursor accepted")
	}
	if _, err := decodeMessageCursor(encodeMessageCursor(messageCursor{V: 2, CreatedAt: time.Now(), ID: uuid.New()})); err == nil {
		t.Fatal("wrong-version timeline cursor accepted")
	}
	if _, err := decodeMessageCursor("not-base64"); err == nil {
		t.Fatal("malformed timeline cursor accepted")
	}
}

func TestEmptyCursorsStartAFirstPage(t *testing.T) {
	if cursor, err := decodeCursor(""); err != nil || cursor != nil {
		t.Fatalf("list start cursor=%v err=%v", cursor, err)
	}
	if cursor, err := decodeMessageCursor(""); err != nil || cursor != nil {
		t.Fatalf("timeline start cursor=%v err=%v", cursor, err)
	}
}
