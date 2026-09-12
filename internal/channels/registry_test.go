package channels

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryRequiresTypedConfigAndRegisteredProviderValidation(t *testing.T) {
	registry := NewRegistry()
	if err := registry.ValidateForEnable(context.Background(), Channel{Type: TypeTelegramBot, Config: json.RawMessage(`{}`), HasCredentials: true}); err == nil {
		t.Fatal("empty typed config enabled")
	}
	if err := registry.ValidateForEnable(context.Background(), Channel{Type: TypeTelegramBot, Config: json.RawMessage(`{"webhook_path":"/inbound"}`), HasCredentials: true}); err == nil {
		t.Fatal("provider-less channel enabled")
	}
}
