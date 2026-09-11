package telegrambot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
)

type lookup struct{ channel channels.Channel }

func (l lookup) Get(context.Context, string) (channels.Channel, error) { return l.channel, nil }

type receiver struct{ got bool }

func (r *receiver) ReceiveInbound(context.Context, core.NormalizedInboundMessage) (core.ReceiveResult, error) {
	r.got = true
	return core.ReceiveResult{}, nil
}

type creds struct{ raw []byte }

func (c creds) Credentials(context.Context, string) ([]byte, error) { return c.raw, nil }

type fakeBot struct {
	meID     int64
	sentTo   int64
	sentText string
}

func TestWebhookNormalizesWithoutLoggingOrProviderCall(t *testing.T) {
	r := &receiver{}
	h := NewWebhookHandler(New(nil), lookup{channels.Channel{ID: "11111111-1111-1111-1111-111111111111", Type: channels.TypeTelegramBot, Enabled: true, Status: channels.StatusActive}}, creds{[]byte(`{"webhook_secret":"secret"}`)}, r)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/telegram-bot/11111111-1111-1111-1111-111111111111", strings.NewReader(`{"update_id":1,"message":{"message_id":2,"chat":{"id":3},"from":{"id":4},"text":"hi"}}`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "secret")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || !r.got {
		t.Fatalf("status=%d called=%v", w.Code, r.got)
	}
}

func TestWebhookRejectsBadSecretBeforeCore(t *testing.T) {
	r := &receiver{}
	h := NewWebhookHandler(New(nil), lookup{channels.Channel{ID: "11111111-1111-1111-1111-111111111111", Type: channels.TypeTelegramBot, Enabled: true, Status: channels.StatusActive}}, creds{[]byte(`{"webhook_secret":"secret"}`)}, r)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/telegram-bot/11111111-1111-1111-1111-111111111111", strings.NewReader(`{`))
	req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "wrong")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || r.got {
		t.Fatalf("status=%d called=%v", w.Code, r.got)
	}
}

func (f *fakeBot) GetMe(context.Context) (User, error) { return User{ID: f.meID}, nil }
func (f *fakeBot) SendMessage(_ context.Context, chatID int64, text string) (SentMessage, error) {
	f.sentTo = chatID
	f.sentText = text
	return SentMessage{ID: 99}, nil
}

func TestValidateNormalizeInboundAndSendWithoutNetwork(t *testing.T) {
	client := &fakeBot{meID: 42}
	adapter := New(client)
	channel := channels.Channel{ID: "11111111-1111-1111-1111-111111111111", Type: channels.TypeTelegramBot, Config: []byte(`{"webhook_path":"/telegram"}`), HasCredentials: true}
	if err := adapter.Validate(context.Background(), channel); err != nil {
		t.Fatal(err)
	}
	inbound, ok, err := adapter.NormalizeUpdate(channel, Update{ID: 7, Message: &Message{ID: 8, Chat: Chat{ID: 10}, From: User{ID: 11, Username: "alice", DisplayName: "Alice"}, Text: "hello"}})
	if err != nil || !ok || inbound.ExternalMessageID != "8" || inbound.ExternalThreadID != "10" || inbound.Sender.ExternalUserID != "11" {
		t.Fatalf("inbound=%+v ok=%v err=%v", inbound, ok, err)
	}
	if _, err := adapter.Send(context.Background(), channel, Outbound{ChatID: "10", Text: "reply"}); err != nil || client.sentTo != 10 || client.sentText != "reply" {
		t.Fatalf("send err=%v client=%+v", err, client)
	}
}
