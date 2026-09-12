package auth

import (
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
)

func TestParseWorkspaceListRejectsUnsafeSelectors(t *testing.T) {
	actor := uuid.New()
	for _, raw := range []string{"?sort=activity_desc", "?limit=0", "?channel_id=invalid", "?assignee=also-invalid"} {
		if _, err := parseWorkspaceList(httptest.NewRequest("GET", "/api/v1/conversations"+raw, nil), actor); err == nil {
			t.Fatalf("accepted invalid query %s", raw)
		}
	}
	q, err := parseWorkspaceList(httptest.NewRequest("GET", "/api/v1/conversations?assignee=me&status=open&priority=high", nil), actor)
	if err != nil || q.Assignee == nil || *q.Assignee != actor {
		t.Fatalf("valid query not parsed: %#v %v", q, err)
	}
}
