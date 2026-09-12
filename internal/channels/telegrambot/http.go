package telegrambot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"net/http"
	"strings"
)

type ChannelLookup interface {
	Get(context.Context, string) (channels.Channel, error)
}
type CredentialLookup interface {
	Credentials(context.Context, string) ([]byte, error)
}
type InboundReceiver interface {
	ReceiveInbound(context.Context, core.NormalizedInboundMessage) (core.ReceiveResult, error)
}
type WebhookHandler struct {
	adapter     *Adapter
	channels    ChannelLookup
	inbound     InboundReceiver
	credentials CredentialLookup
}

func NewWebhookHandler(adapter *Adapter, lookup ChannelLookup, credentials CredentialLookup, inbound InboundReceiver) http.Handler {
	return &WebhookHandler{adapter: adapter, channels: lookup, credentials: credentials, inbound: inbound}
}
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/webhooks/telegram-bot/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}
	channel, err := h.channels.Get(r.Context(), id)
	if err != nil || channel.Type != channels.TypeTelegramBot {
		http.NotFound(w, r)
		return
	}
	if !channel.Enabled || channel.Status != channels.StatusActive {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if h.credentials == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	raw, err := h.credentials.Credentials(r.Context(), id)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var credentials Credentials
	if json.Unmarshal(raw, &credentials) != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	if credentials.WebhookSecret != "" && subtle.ConstantTimeCompare([]byte(credentials.WebhookSecret), []byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token"))) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var update Update
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&update); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	normalized, ok, err := h.adapter.NormalizeUpdate(channel, update)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err = h.inbound.ReceiveInbound(r.Context(), normalized); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
