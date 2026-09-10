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
	var messages, events, channelConsistent int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE id=$1`, result.MessageID).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1 AND dispatched_at IS NULL`, result.MessageID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messages m JOIN conversations c ON c.id=m.conversation_id JOIN contact_identities ci ON ci.id=c.contact_identity_id WHERE m.id=$1 AND m.channel_id=c.channel_id AND c.channel_id=ci.channel_id AND c.contact_id=ci.contact_id`, result.MessageID).Scan(&channelConsistent); err != nil {
		t.Fatal(err)
	}
	if messages != 1 || events < 1 || channelConsistent != 1 {
		t.Fatalf("messages=%d events=%d channel_consistent=%d", messages, events, channelConsistent)
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
	changedDelivery := input
	changedDelivery.ExternalThreadID = "other-thread-" + uuid.NewString()
	changedDelivery.Sender = core.ExternalSender{ExternalUserID: "other-customer-" + uuid.NewString(), DisplayName: "Unexpected retry sender"}
	second, err := service.ReceiveInbound(ctx, changedDelivery)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Duplicate || first.MessageID != second.MessageID || first.ConversationID != second.ConversationID || first.IdentityID != second.IdentityID || first.ContactID != second.ContactID {
		t.Fatalf("duplicate result: first=%+v second=%+v", first, second)
	}
	var contacts, messages, identities, conversations, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM contacts c WHERE EXISTS (SELECT 1 FROM contact_identities ci WHERE ci.channel_id=$1 AND ci.contact_id=c.id)), (SELECT count(*) FROM messages WHERE channel_id=$1), (SELECT count(*) FROM contact_identities WHERE channel_id=$1), (SELECT count(*) FROM conversations WHERE channel_id=$1), (SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM messages WHERE channel_id=$1) OR aggregate_id IN (SELECT id FROM conversations WHERE channel_id=$1))`, channelID).Scan(&contacts, &messages, &identities, &conversations, &events); err != nil {
		t.Fatal(err)
	}
	if contacts != 1 || messages != 1 || identities != 1 || conversations != 1 || events != 2 {
		t.Fatalf("duplicate created facts: contacts=%d messages=%d identities=%d conversations=%d events=%d", contacts, messages, identities, conversations, events)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM conversations WHERE id=$1`, first.ConversationID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("duplicate incremented conversation version=%d", version)
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
	inputs := []core.NormalizedInboundMessage{input, input}
	inputs[1].ExternalThreadID = "concurrent-other-thread-" + uuid.NewString()
	inputs[1].Sender = core.ExternalSender{ExternalUserID: "concurrent-other-customer-" + uuid.NewString()}
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
			results[index], errs[index] = service.ReceiveInbound(ctx, inputs[index])
		}(i)
	}
	close(start)
	wait.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].MessageID != results[1].MessageID || results[0].ConversationID != results[1].ConversationID || results[0].IdentityID != results[1].IdentityID || results[0].ContactID != results[1].ContactID || !results[0].Duplicate && !results[1].Duplicate {
		t.Fatalf("race results=%+v %+v", results[0], results[1])
	}
	var contacts, messages, identities, conversations, events int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM contacts c WHERE EXISTS (SELECT 1 FROM contact_identities ci WHERE ci.channel_id=$1 AND ci.contact_id=c.id)), (SELECT count(*) FROM messages WHERE channel_id=$1 AND external_message_id=$2), (SELECT count(*) FROM contact_identities WHERE channel_id=$1), (SELECT count(*) FROM conversations WHERE channel_id=$1), (SELECT count(*) FROM outbox_events WHERE aggregate_id IN (SELECT id FROM messages WHERE channel_id=$1) OR aggregate_id IN (SELECT id FROM conversations WHERE channel_id=$1))`, channelID, input.ExternalMessageID).Scan(&contacts, &messages, &identities, &conversations, &events); err != nil {
		t.Fatal(err)
	}
	if contacts != 1 || messages != 1 || identities != 1 || conversations != 1 || events != 2 {
		t.Fatalf("race facts: contacts=%d messages=%d identities=%d conversations=%d events=%d", contacts, messages, identities, conversations, events)
	}
	var version int64
	if err := pool.QueryRow(ctx, `SELECT version FROM conversations WHERE id=$1`, results[0].ConversationID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("race incremented conversation version=%d", version)
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

func TestReceiveInboundRejectsInvalidNormalizedMessage(t *testing.T) {
	result, err := core.NewReceiveInboundService(nil).ReceiveInbound(context.Background(), core.NormalizedInboundMessage{})
	if err != core.ErrInvalidInbound || result != (core.ReceiveResult{}) {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestReceiveInboundRejectsInconsistentCanonicalMessagePath(t *testing.T) {
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
	channelID, otherChannelID := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{channelID, otherChannelID} {
		if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Core consistency','active',true)`, id); err != nil {
			t.Fatal(err)
		}
	}
	defer cleanupCoreChannel(t, pool, channelID)
	defer cleanupCoreChannel(t, pool, otherChannelID)
	input := core.NormalizedInboundMessage{ChannelID: channelID, ExternalMessageID: "message-" + uuid.NewString(), ExternalThreadID: "thread-" + uuid.NewString(), Sender: core.ExternalSender{ExternalUserID: "customer-" + uuid.NewString()}, Text: "consistency", ContentType: core.ContentText}
	service := core.NewReceiveInboundService(pool)
	first, err := service.ReceiveInbound(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversations SET channel_id=$2 WHERE id=$1`, first.ConversationID, otherChannelID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `UPDATE conversations SET channel_id=$2 WHERE id=$1`, first.ConversationID, channelID)
	}()
	if _, err := service.ReceiveInbound(ctx, input); err == nil || err.Error() != "canonical message channel mismatch" {
		t.Fatalf("error=%v", err)
	}
}
