package channels

import "sort"

type Type string

const (
	TypeEmail        Type = "email"    // Existing channel type; retained for compatibility.
	TypeTelegram     Type = "telegram" // Existing legacy channel type; retained for compatibility.
	TypeTelegramBot  Type = "telegram_bot"
	TypeTelegramUser Type = "telegram_user"
	TypeCarrotquest  Type = "carrotquest"
	TypeOmni         Type = "omni"
	TypeMAX          Type = "max"
	TypeVK           Type = "vk"
)

// Registry is private and never augmented from request data, so a provider
// type is an immutable product-level capability rather than an admin setting.
var registry = map[Type]struct{}{
	TypeEmail: {}, TypeTelegram: {}, TypeTelegramBot: {}, TypeTelegramUser: {},
	TypeCarrotquest: {}, TypeOmni: {}, TypeMAX: {}, TypeVK: {},
}

func ValidType(t Type) bool { _, ok := registry[t]; return ok }

func Types() []Type {
	result := make([]Type, 0, len(registry))
	for t := range registry {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

type Status string

const (
	StatusActive                  Status = "active"
	StatusDegraded                Status = "degraded"
	StatusReauthorizationRequired Status = "reauthorization_required"
	StatusDisabled                Status = "disabled"
	StatusError                   Status = "error"
)
