package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSecrets(t *testing.T) {
	var b bytes.Buffer
	l := New(&b, "api", "test")
	l.Info("request", slog.String("password", "secret-one"), slog.Group("nested", slog.String("Authorization", "secret-two")), slog.Any("data", map[string]any{"token": "secret-three"}))
	for _, s := range []string{"secret-one", "secret-two", "secret-three"} {
		if strings.Contains(b.String(), s) {
			t.Fatal("secret leaked")
		}
	}
}
