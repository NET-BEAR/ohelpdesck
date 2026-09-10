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

type transitionFailingAppender struct{}

func (transitionFailingAppender) Append(context.Context, pgx.Tx, ...outbox.Event) error {
	return errors.New("injected transition outbox failure")
}

func transitionFixture(t *testing.T) (context.Context, *database.Pool, uuid.UUID, core.ReceiveResult) {
	t.Helper()
	requireCoreDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	channelID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO channels(id,type,name,status,enabled) VALUES($1,'email','Transition integration','active',true)`, channelID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupCoreChannel(t, pool, channelID) })
	result, err := core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{
		ChannelID: channelID, ExternalMessageID: "transition-message-" + uuid.NewString(), ExternalThreadID: "transition-thread-" + uuid.NewString(),
		Sender: core.ExternalSender{ExternalUserID: "transition-customer-" + uuid.NewString()}, Text: "help", ContentType: core.ContentText,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, pool, channelID, result
}

func setTransitionState(t *testing.T, ctx context.Context, pool *database.Pool, conversationID uuid.UUID, status core.ConversationStatus) {
	t.Helper()
	var resolvedAt, snoozedUntil any
	if status == core.ConversationResolved {
		resolvedAt = time.Now().UTC().Add(-time.Minute)
	}
	if status == core.ConversationSnoozed {
		snoozedUntil = time.Now().UTC().Add(time.Hour)
	}
	if _, err := pool.Exec(ctx, `UPDATE conversations SET status=$2,resolved_at=$3,snoozed_until=$4,version=1 WHERE id=$1`, conversationID, status, resolvedAt, snoozedUntil); err != nil {
		t.Fatal(err)
	}
}

func conversationSnapshot(t *testing.T, ctx context.Context, pool *database.Pool, conversationID uuid.UUID) (core.Conversation, int) {
	t.Helper()
	var got core.Conversation
	var events int
	if err := pool.QueryRow(ctx, `SELECT id,channel_id,status,priority,resolved_at,snoozed_until,version FROM conversations WHERE id=$1`, conversationID).Scan(&got.ID, &got.ChannelID, &got.Status, &got.Priority, &got.ResolvedAt, &got.SnoozedUntil, &got.Version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE aggregate_id=$1`, conversationID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	return got, events
}

func TestConversationStatusTransitionMatrix(t *testing.T) {
	cases := []struct{ from, to core.ConversationStatus }{
		{core.ConversationOpen, core.ConversationPending}, {core.ConversationOpen, core.ConversationResolved}, {core.ConversationOpen, core.ConversationSnoozed},
		{core.ConversationPending, core.ConversationOpen}, {core.ConversationPending, core.ConversationResolved}, {core.ConversationPending, core.ConversationSnoozed},
		{core.ConversationSnoozed, core.ConversationOpen}, {core.ConversationSnoozed, core.ConversationResolved}, {core.ConversationResolved, core.ConversationOpen},
	}
	for _, tc := range cases {
		t.Run(string(tc.from)+"_to_"+string(tc.to), func(t *testing.T) {
			ctx, pool, _, fixture := transitionFixture(t)
			setTransitionState(t, ctx, pool, fixture.ConversationID, tc.from)
			before, beforeEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
			var until *time.Time
			if tc.to == core.ConversationSnoozed {
				value := time.Now().UTC().Add(time.Hour)
				until = &value
			}
			got, err := core.NewConversationService(pool).ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: tc.to, SnoozedUntil: until})
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.to || got.Version != before.Version+1 {
				t.Fatalf("result=%+v", got)
			}
			after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
			if after.Status != tc.to || after.Version != before.Version+1 || afterEvents != beforeEvents+1 {
				t.Fatalf("after=%+v events=%d beforeEvents=%d", after, afterEvents, beforeEvents)
			}
			if tc.to == core.ConversationResolved && after.ResolvedAt == nil {
				t.Fatal("resolved_at missing")
			}
			if tc.to != core.ConversationResolved && after.ResolvedAt != nil {
				t.Fatal("resolved_at was not cleared")
			}
			if tc.to == core.ConversationSnoozed && after.SnoozedUntil == nil {
				t.Fatal("snoozed_until missing")
			}
			if tc.to != core.ConversationSnoozed && after.SnoozedUntil != nil {
				t.Fatal("snoozed_until was not cleared")
			}
		})
	}
}

func TestConversationTransitionRejectsInvalidWithoutMutation(t *testing.T) {
	ctx, pool, _, fixture := transitionFixture(t)
	setTransitionState(t, ctx, pool, fixture.ConversationID, core.ConversationResolved)
	before, beforeEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	_, err := core.NewConversationService(pool).ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationPending})
	if !errors.Is(err, core.ErrInvalidConversationTransition) {
		t.Fatalf("error=%v", err)
	}
	after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if !sameConversation(after, before) || afterEvents != beforeEvents {
		t.Fatalf("mutation after invalid transition: before=%+v/%d after=%+v/%d", before, beforeEvents, after, afterEvents)
	}
}

func TestConversationResolveUsesDBTimeAndEmitsNewVersion(t *testing.T) {
	ctx, pool, _, fixture := transitionFixture(t)
	before, beforeEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	var lower, upper time.Time
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&lower); err != nil {
		t.Fatal(err)
	}
	got, err := core.NewConversationService(pool).ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationResolved})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&upper); err != nil {
		t.Fatal(err)
	}
	if got.ResolvedAt == nil || got.ResolvedAt.Before(lower) || got.ResolvedAt.After(upper) {
		t.Fatalf("resolved_at=%v outside database clock interval [%v,%v]", got.ResolvedAt, lower, upper)
	}
	var eventVersion int64
	if err := pool.QueryRow(ctx, `SELECT (payload->>'version')::bigint FROM outbox_events WHERE aggregate_id=$1 AND event_type='conversation.status_changed' ORDER BY created_at DESC LIMIT 1`, fixture.ConversationID).Scan(&eventVersion); err != nil {
		t.Fatal(err)
	}
	if got.Version != before.Version+1 || eventVersion != got.Version {
		t.Fatalf("result=%+v event_version=%d", got, eventVersion)
	}
	_, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if afterEvents != beforeEvents+1 {
		t.Fatalf("outbox events=%d want %d", afterEvents, beforeEvents+1)
	}
}

func TestConversationRejectsStaleVersionAfterInbound(t *testing.T) {
	ctx, pool, channelID, fixture := transitionFixture(t)
	before, beforeEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	var thread, externalUserID string
	if err := pool.QueryRow(ctx, `SELECT c.external_thread_id,ci.external_user_id FROM conversations c JOIN contact_identities ci ON ci.id=c.contact_identity_id WHERE c.id=$1`, fixture.ConversationID).Scan(&thread, &externalUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := core.NewReceiveInboundService(pool).ReceiveInbound(ctx, core.NormalizedInboundMessage{
		ChannelID: channelID, ExternalMessageID: "later-message-" + uuid.NewString(), ExternalThreadID: thread,
		Sender: core.ExternalSender{ExternalUserID: externalUserID}, Text: "new inbound", ContentType: core.ContentText,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := core.NewConversationService(pool).ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationResolved})
	if !errors.Is(err, core.ErrVersionConflict) {
		t.Fatalf("error=%v", err)
	}
	after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if after.Status != core.ConversationOpen || after.Version != before.Version+1 || afterEvents != beforeEvents+1 {
		t.Fatalf("unexpected stale result: %+v events=%d", after, afterEvents)
	}
}

func TestConversationSnoozeRequiresFutureDeadline(t *testing.T) {
	ctx, pool, _, fixture := transitionFixture(t)
	service := core.NewConversationService(pool)
	before, events := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	for _, deadline := range []*time.Time{nil, ptrTime(time.Now().UTC().Add(-time.Minute))} {
		_, err := service.ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationSnoozed, SnoozedUntil: deadline})
		if !errors.Is(err, core.ErrInvalidSnoozeDeadline) {
			t.Fatalf("deadline=%v error=%v", deadline, err)
		}
		after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
		if !sameConversation(after, before) || afterEvents != events {
			t.Fatal("invalid deadline mutated conversation")
		}
	}
	deadline := time.Now().UTC().Add(time.Hour)
	got, err := service.ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationSnoozed, SnoozedUntil: &deadline})
	if err != nil || got.SnoozedUntil == nil || got.Status != core.ConversationSnoozed {
		t.Fatalf("snooze result=%+v error=%v", got, err)
	}
}

func TestConversationPriorityIsIndependent(t *testing.T) {
	ctx, pool, _, fixture := transitionFixture(t)
	setTransitionState(t, ctx, pool, fixture.ConversationID, core.ConversationPending)
	if _, err := pool.Exec(ctx, `UPDATE conversations SET waiting_since=clock_timestamp(),last_activity_at=clock_timestamp() WHERE id=$1`, fixture.ConversationID); err != nil {
		t.Fatal(err)
	}
	service := core.NewConversationService(pool)
	for _, priority := range []core.ConversationPriority{core.ConversationLow, core.ConversationNormal, core.ConversationHigh, core.ConversationUrgent} {
		before, events := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
		got, err := service.SetPriority(ctx, core.SetConversationPriority{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Priority: priority})
		if err != nil {
			t.Fatal(err)
		}
		if got.Priority != priority || got.Status != before.Status || got.Version != before.Version+1 {
			t.Fatalf("result=%+v before=%+v", got, before)
		}
		after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
		if after.Status != before.Status || after.ResolvedAt != before.ResolvedAt || after.SnoozedUntil != before.SnoozedUntil || afterEvents != events+1 {
			t.Fatalf("priority changed lifecycle: before=%+v after=%+v", before, after)
		}
	}
}

func TestConversationTransitionRollsBackWhenOutboxFails(t *testing.T) {
	ctx, pool, _, fixture := transitionFixture(t)
	before, events := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	_, err := core.NewConversationService(pool, transitionFailingAppender{}).ChangeStatus(ctx, core.ChangeConversationStatus{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationResolved})
	if err == nil || err.Error() != "injected transition outbox failure" {
		t.Fatalf("error=%v", err)
	}
	after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if !sameConversation(after, before) || afterEvents != events {
		t.Fatalf("rollback failed: before=%+v/%d after=%+v/%d", before, events, after, afterEvents)
	}
}

func TestConversationTransitionConcurrentCommandsUseExpectedVersion(t *testing.T) {
	ctx, pool, _, fixture := transitionFixture(t)
	before, events := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	service := core.NewConversationService(pool)
	commands := []core.ChangeConversationStatus{
		{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationPending},
		{ConversationID: fixture.ConversationID, ExpectedVersion: before.Version, Status: core.ConversationResolved},
	}
	start := make(chan struct{})
	errs := make([]error, len(commands))
	var wait sync.WaitGroup
	for i := range commands {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			_, errs[i] = service.ChangeStatus(ctx, commands[i])
		}(i)
	}
	close(start)
	wait.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("errors=%v", errs)
	}
	if errs[0] != nil && !errors.Is(errs[0], core.ErrVersionConflict) || errs[1] != nil && !errors.Is(errs[1], core.ErrVersionConflict) {
		t.Fatalf("errors=%v", errs)
	}
	after, afterEvents := conversationSnapshot(t, ctx, pool, fixture.ConversationID)
	if after.Version != before.Version+1 || afterEvents != events+1 {
		t.Fatalf("concurrent mutation result=%+v events=%d", after, afterEvents)
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

func sameConversation(left, right core.Conversation) bool {
	return left.ID == right.ID && left.ChannelID == right.ChannelID && left.Status == right.Status && left.Priority == right.Priority && left.Version == right.Version && sameTime(left.ResolvedAt, right.ResolvedAt) && sameTime(left.SnoozedUntil, right.SnoozedUntil)
}

func sameTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
