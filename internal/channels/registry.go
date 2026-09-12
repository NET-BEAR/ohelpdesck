package channels

import (
	"context"
	"encoding/json"
)

// ProviderValidator is implemented by a real adapter. Configuration-only
// validation is deliberately insufficient to activate a channel.
type ProviderValidator interface {
	Validate(context.Context, Channel) error
}
type Registry struct {
	required  map[Type][]string
	providers map[Type]ProviderValidator
}

func NewRegistry() *Registry {
	return &Registry{required: map[Type][]string{
		TypeTelegramBot: {"webhook_path"}, TypeTelegramUser: {"api_id"}, TypeCarrotquest: {"endpoint"}, TypeOmni: {"base_url", "sender"}, TypeMAX: {"webhook_path"}, TypeVK: {"group_id", "callback_path"},
	}, providers: map[Type]ProviderValidator{}}
}
func (r *Registry) Register(t Type, provider ProviderValidator) {
	if r != nil && provider != nil && ValidType(t) {
		r.providers[t] = provider
	}
}
func (r *Registry) ValidateForEnable(ctx context.Context, channel Channel) error {
	if !channel.HasCredentials || !ValidType(channel.Type) {
		return ErrCredentialsMissing
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(channel.Config, &config) != nil {
		return ErrInvalid
	}
	for _, key := range r.required[channel.Type] {
		var value string
		if json.Unmarshal(config[key], &value) != nil || value == "" {
			return ErrInvalid
		}
	}
	provider := r.providers[channel.Type]
	if provider == nil {
		return ErrProviderValidationUnavailable
	}
	return provider.Validate(ctx, channel)
}
