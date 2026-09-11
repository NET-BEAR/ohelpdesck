package channels

import "testing"

func TestRegistryContainsAdaptersAndLegacyChannelTypes(t *testing.T) {
	expected := []Type{TypeTelegramBot, TypeTelegramUser, TypeCarrotquest, TypeOmni, TypeMAX, TypeVK, TypeEmail, TypeTelegram}
	for _, channelType := range expected {
		if !ValidType(channelType) {
			t.Fatalf("required type %q is absent", channelType)
		}
	}
	if ValidType("attacker_controlled_type") {
		t.Fatal("registry accepted a request-defined type")
	}
}
