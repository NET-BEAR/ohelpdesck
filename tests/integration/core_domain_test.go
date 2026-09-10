package integration

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/outbox"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func requireCoreDatabase(t *testing.T) {
	t.Helper()
	if os.Getenv("DATABASE_URL") == "" {
		t.Skip("integration database absent")
	}
}

func cleanupCoreChannel(t *testing.T, pool *database.Pool, channelID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT contact_id FROM contact_identities WHERE channel_id=$1`, channelID)
	if err != nil {
		t.Error(err)
		return
	}
	contactIDs := make([]uuid.UUID, 0)
	for rows.Next() {
		var contactID uuid.UUID
		if err := rows.Scan(&contactID); err != nil {
			rows.Close()
			t.Error(err)
			return
		}
		contactIDs = append(contactIDs, contactID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Error(err)
		return
	}
	rows.Close()
	if _, err := pool.Exec(ctx, `DELETE FROM outbox_events WHERE aggregate_id IN (SELECT id FROM messages WHERE channel_id=$1) OR aggregate_id IN (SELECT id FROM conversations WHERE channel_id=$1)`, channelID); err != nil {
		t.Error(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM messages WHERE channel_id=$1`, channelID); err != nil {
		t.Error(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM conversations WHERE channel_id=$1`, channelID); err != nil {
		t.Error(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM contact_identities WHERE channel_id=$1`, channelID); err != nil {
		t.Error(err)
	}
	if len(contactIDs) > 0 {
		if _, err := pool.Exec(ctx, `DELETE FROM contacts WHERE id = ANY($1) AND NOT EXISTS (SELECT 1 FROM contact_identities WHERE contact_id=contacts.id)`, contactIDs); err != nil {
			t.Error(err)
		}
	}
	if _, err := pool.Exec(ctx, `DELETE FROM channels WHERE id=$1`, channelID); err != nil {
		t.Error(err)
	}
}

func TestReceiveInboundCreatesCoreFactsAndOutbox(t *testing.T) {
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}

	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Core integration','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	defer cleanupCoreChannel(t, pool, channelID)

	service := core.NewReceiveInboundService(pool)
	result, err := service.ReceiveInbound(ctx, core.NormalizedInboundMessage{
		ChannelID: channelID, ProviderEventID: "event-" + uuid.NewString(), ExternalMessageID: "message-" + uuid.NewString(),
		ExternalThreadID: "thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "customer-" + uuid.NewString(), DisplayName: "Customer"}, Text: "need help", ContentType: core.ContentText,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContactID == uuid.Nil || result.IdentityID == uuid.Nil || result.ConversationID == uuid.Nil || result.MessageID == uuid.Nil {
		t.Fatalf("canonical IDs missing: %+v", result)
	}
	var messages, events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1`, result.MessageID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND dispatched_at IS NULL`, result.MessageID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || events < 1 {
		t.Fatalf("messages=%d events=%d", messages, events)
	}
}

func TestReceiveInboundDuplicateDoesNotCreateAdditionalFacts(t *testing.T) {
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Core duplicate','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	defer cleanupCoreChannel(t, pool, channelID)
	input := core.NormalizedInboundMessage{ChannelID: channelID, ProviderEventID: "event-" + uuid.NewString(), ExternalMessageID: "message-" + uuid.NewString(), ExternalThreadID: "thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "customer-" + uuid.NewString()}, Text: "same", ContentType: core.ContentText}
	service := core.NewReceiveInboundService(pool)
	first, err := service.ReceiveInbound(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ReceiveInbound(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || first.MessageID != second.MessageID || first.ConversationID != second.ConversationID {
		t.Fatalf("duplicate result: first=%+v second=%+v", first, second)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE conversation_id=$1`, first.ConversationID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("message count=%d", count)
	}
}

func TestReceiveInboundRejectsInactiveChannelWithoutFacts(t *testing.T) {
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Inactive','disabled',false)`, channelID); err != nil {
		t.Fatal(err)
	}
	defer cleanupCoreChannel(t, pool, channelID)
	_, err = core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{ChannelID: channelID, ExternalMessageID: "message-" + uuid.NewString(), ExternalThreadID: "thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "customer-" + uuid.NewString()}, ContentType: core.ContentText})
	if err != core.ErrChannelDisabled {
		t.Fatalf("error=%v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM contact_identities WHERE channel_id=$1`, channelID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("identities=%d", count)
	}
}

func TestReceiveInboundDuplicateRaceReturnsCanonicalMessage(t *testing.T) {
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Core race','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	defer cleanupCoreChannel(t, pool, channelID)
	input := core.NormalizedInboundMessage{ChannelID: channelID, ExternalMessageID: "message-" + uuid.NewString(), ExternalThreadID: "thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "customer-" + uuid.NewString()}, Text: "race", ContentType: core.ContentText}
	service := core.NewReceiveInboundService(pool)
	start := make(chan struct{})
	results := make([]core.ReceiveResult, 2)
	errs := make([]error, 2)
	var wait sync.WaitGroup
	for i := range results {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errs[index] = service.ReceiveInbound(ctx, input)
		}(i)
	}
	close(start)
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].MessageID != results[1].MessageID || !results[0].Duplicate && !results[1].Duplicate {
		t.Fatalf("race results=%+v %+v", results[0], results[1])
	}
	var messages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE channel_id=$1 AND external_message_id=$2`, channelID, input.ExternalMessageID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if messages != 1 {
		t.Fatalf("messages=%d", messages)
	}
}

type failingAppender struct{}

func (failingAppender) Append(context.Context, pgx.Tx, ...outbox.Event) error {
	return errors.New("injected outbox failure")
}

func TestReceiveInboundRollsBackWhenOutboxAppendFails(t *testing.T) {
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Core rollback','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	defer cleanupCoreChannel(t, pool, channelID)
	input := core.NormalizedInboundMessage{ChannelID: channelID, ExternalMessageID: "message-" + uuid.NewString(), ExternalThreadID: "thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "customer-" + uuid.NewString()}, Text: "rollback", ContentType: core.ContentText}
	_, err = core.NewReceiveInboundService(pool, failingAppender{}).ReceiveInbound(ctx, input)
	if err == nil || err.Error() != "injected outbox failure" {
		t.Fatalf("error=%v", err)
	}
	var messages, identities, conversations, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM messages WHERE channel_id=$1), (SELECT count(*) FROM contact_identities WHERE channel_id=$1), (SELECT count(*) FROM conversations WHERE channel_id=$1), (SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM messages WHERE channel_id=$1))`, channelID).Scan(&messages, &identities, &conversations, &events); err != nil {
		t.Fatal(err)
	}
	if messages != 0 || identities != 0 || conversations != 0 || events != 0 {
		t.Fatalf("partial durable state messages=%d identities=%d conversations=%d events=%d", messages, identities, conversations, events)
	}
}
