package runtime

import (
	"context"
	"testing"
)

func TestMissingConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if Run(context.Background(), false) == nil {
		t.Fatal("missing configuration")
	}
}
