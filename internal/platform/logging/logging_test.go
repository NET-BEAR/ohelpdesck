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
func TestSecretGroupAncestors(t *testing.T) {
	var b bytes.Buffer
	l := New(&b, "api", "test")
	l.Info("request", slog.Group("authorization", slog.String("value", "group-secret")))
	l.WithGroup("password").Info("request", "value", "ancestor-secret")
	if strings.Contains(b.String(), "group-secret") || strings.Contains(b.String(), "ancestor-secret") {
		t.Fatal("secret group ancestry leaked")
	}
}
