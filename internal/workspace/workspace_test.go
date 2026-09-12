package workspace

import (
	"context"
	"errors"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestListCursorRoundTripAndValidation(t *testing.T) {
	id := uuid.New()
	raw := encodeCursor(listCursor{V: 2, Number: 42, ID: id, Filters: canonicalListFilters(ListQuery{Statuses: []core.ConversationStatus{core.ConversationOpen}})})
	got, err := decodeCursor(raw)
	if err != nil || got.Number != 42 || got.ID != id || !matchesListCursorFilters(got, ListQuery{Statuses: []core.ConversationStatus{core.ConversationOpen}}) {
		t.Fatalf("cursor round trip failed: %#v %v", got, err)
	}
	if matchesListCursorFilters(got, ListQuery{Statuses: []core.ConversationStatus{core.ConversationPending}}) {
		t.Fatal("cursor matched different filters")
	}
	for _, bad := range []string{"not-a-cursor", encodeCursor(listCursor{V: 1, Number: 42, ID: id}), encodeCursor(listCursor{V: 2, Number: 0, ID: id})} {
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

func TestReadServiceRejectsMissingDependenciesAndIDs(t *testing.T) {
	service := NewService(nil)
	actor, conversation := uuid.New(), uuid.New()
	if _, err := service.List(context.Background(), actor, ListQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("list without db=%v", err)
	}
	if _, err := service.Detail(context.Background(), actor, conversation, false, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("detail without db=%v", err)
	}
	if _, err := service.Messages(context.Background(), actor, conversation, "", "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("messages without db=%v", err)
	}
	if _, err := NewService(nil).List(context.Background(), uuid.Nil, ListQuery{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("nil actor=%v", err)
	}
}
