// Package telegrambot provides a provider-neutral boundary around a Telegram
// Bot API client. A concrete GoTd-backed client can satisfy Client; tests use
// the same interface without network access.
package telegrambot

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/google/uuid"
)

type Config struct {
	WebhookPath string `json:"webhook_path"`
}
type Credentials struct {
	BotToken      string `json:"bot_token"`
	WebhookSecret string `json:"webhook_secret"`
}
type User struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"first_name"`
}
type Chat struct {
	ID int64 `json:"id"`
}
type Message struct {
	ID   int64  `json:"message_id"`
	Chat Chat   `json:"chat"`
	From User   `json:"from"`
	Text string `json:"text"`
}
type Update struct {
	ID      int64    `json:"update_id"`
	Message *Message `json:"message"`
}
type SentMessage struct{ ID int64 }
type Client interface {
	GetMe(context.Context) (User, error)
	SendMessage(context.Context, int64, string) (SentMessage, error)
}
type Adapter struct{ client Client }

func New(client Client) *Adapter { return &Adapter{client: client} }

func (a *Adapter) Validate(ctx context.Context, channel channels.Channel) error {
	if a == nil || a.client == nil {
		return fmt.Errorf("telegram bot client unavailable")
	}
	if channel.Type != channels.TypeTelegramBot || !channel.HasCredentials {
		return channels.ErrCredentialsMissing
	}
	var config Config
	if !decodeConfig(channel.Config, &config) || !strings.HasPrefix(config.WebhookPath, "/") {
		return channels.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 5_000_000_000)
	defer cancel()
	me, err := a.client.GetMe(bounded)
	if err != nil {
		return fmt.Errorf("telegram bot validation failed")
	}
	if me.ID == 0 {
		return fmt.Errorf("telegram bot identity invalid")
	}
	return nil
}
func (a *Adapter) NormalizeUpdate(channel channels.Channel, update Update) (core.NormalizedInboundMessage, bool, error) {
	if channel.Type != channels.TypeTelegramBot || update.Message == nil {
		return core.NormalizedInboundMessage{}, false, nil
	}
	m := update.Message
	if m.ID == 0 || m.Chat.ID == 0 || m.From.ID == 0 || strings.TrimSpace(m.Text) == "" {
		return core.NormalizedInboundMessage{}, false, channels.ErrInvalid
	}
	channelID, err := uuid.Parse(channel.ID)
	if err != nil {
		return core.NormalizedInboundMessage{}, false, channels.ErrInvalid
	}
	return core.NormalizedInboundMessage{ChannelID: channelID, ProviderEventID: strconv.FormatInt(update.ID, 10), ExternalMessageID: strconv.FormatInt(m.ID, 10), ExternalThreadID: strconv.FormatInt(m.Chat.ID, 10), Sender: core.ExternalSender{ExternalUserID: strconv.FormatInt(m.From.ID, 10), ExternalChatID: strconv.FormatInt(m.Chat.ID, 10), Username: m.From.Username, DisplayName: m.From.DisplayName}, Text: m.Text, ContentType: core.ContentText}, true, nil
}

type Outbound struct{ ChatID, Text string }
type OutboundResult struct {
	ProviderMessageID      string
	ReconciliationRequired bool
}

func (a *Adapter) Send(ctx context.Context, channel channels.Channel, outbound Outbound) (OutboundResult, error) {
	if channel.Type != channels.TypeTelegramBot || a == nil || a.client == nil || strings.TrimSpace(outbound.Text) == "" {
		return OutboundResult{}, channels.ErrInvalid
	}
	chatID, err := strconv.ParseInt(outbound.ChatID, 10, 64)
	if err != nil || chatID == 0 {
		return OutboundResult{}, channels.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 5_000_000_000)
	defer cancel()
	sent, err := a.client.SendMessage(bounded, chatID, outbound.Text)
	if err != nil {
		return OutboundResult{}, fmt.Errorf("telegram bot send failed")
	}
	return OutboundResult{ProviderMessageID: strconv.FormatInt(sent.ID, 10), ReconciliationRequired: false}, nil
}

// Reconcile reports that Bot API has no message delivery status endpoint; the
// worker must retain its canonical sent result rather than retry blindly.
func (a *Adapter) Reconcile(context.Context, channels.Channel, string) (OutboundResult, error) {
	return OutboundResult{ReconciliationRequired: false}, nil
}
func decodeConfig(raw []byte, target any) bool { return json.Unmarshal(raw, target) == nil }
