package integration

import (
	"context"
	"errors"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/redis"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/runtime"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/storage"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func required(t *testing.T) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "REDIS_URL", "S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if os.Getenv(k) == "" {
			if os.Getenv("REQUIRE_INTEGRATION") == "true" {
				t.Fatalf("required integration configuration missing: %s", k)
			}
			t.Skip("integration environment absent")
		}
	}
}
func TestFoundation(t *testing.T) {
	required(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p, e := database.Open(ctx, os.Getenv("DATABASE_URL"), 3)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	for _, d := range []string{"down", "status", "up", "up", "status"} {
		v, e := p.Migrate(ctx, d)
		if e != nil {
			t.Fatal(e)
		}
		want := 2
		if d == "status" && v == 1 { // status directly after one safe rollback
			want = 1
		}
		if (d == "up" || d == "status" && v != 0) && v != want {
			t.Fatalf("migration %s: got %d want %d", d, v, want)
		}
	}
	if _, e = p.Migrate(ctx, "invalid"); e == nil {
		t.Fatal("bad direction")
	}
	sentinel := errors.New("rollback")
	e = p.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, e := tx.Exec(ctx, "INSERT INTO schema_migrations(version) VALUES(99)")
		if e != nil {
			return e
		}
		return sentinel
	})
	if !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	var count int
	if e = p.QueryRow(ctx, "SELECT count(*) FROM schema_migrations WHERE version=99").Scan(&count); e != nil || count != 0 {
		t.Fatal("rollback failed")
	}
	if e = p.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error { return tx.QueryRow(ctx, "SELECT 1").Scan(&count) }); e != nil {
		t.Fatal(e)
	}
	cache, e := redis.Open(os.Getenv("REDIS_URL"))
	if e != nil {
		t.Fatal(e)
	}
	defer cache.Close()
	if e = cache.Health(ctx); e != nil {
		t.Fatal(e)
	}
	s, e := storage.New(os.Getenv("S3_ENDPOINT"), os.Getenv("S3_BUCKET"), os.Getenv("S3_ACCESS_KEY"), os.Getenv("S3_SECRET_KEY"), false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Health(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "integration-foundation", strings.NewReader("test"), 4, "text/plain"); e != nil {
		t.Fatal(e)
	}
	reader, e := s.Get(ctx, "integration-foundation")
	if e != nil {
		t.Fatal(e)
	}
	body, e := io.ReadAll(reader)
	_ = reader.Close()
	if e != nil || string(body) != "test" {
		t.Fatal("object mismatch")
	}
	if _, e = s.PresignGet(ctx, "integration-foundation", time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(ctx, "integration-foundation"); e != nil {
		t.Fatal(e)
	}
}
func TestRuntimeLifecycle(t *testing.T) {
	required(t)
	t.Setenv("HTTP_ADDRESS", "127.0.0.1:0")
	t.Setenv("METRICS_ADDRESS", "127.0.0.1:0")
	for _, worker := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runtime.Run(ctx, worker) }()
		time.Sleep(300 * time.Millisecond)
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Fatal(e)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("shutdown timeout")
		}
	}
}

func TestRuntimeFailures(t *testing.T) {
	required(t)
	t.Setenv("METRICS_ADDRESS", "bad address")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if runtime.Run(ctx, true) == nil {
		t.Fatal("invalid listener accepted")
	}
	t.Setenv("DATABASE_URL", "postgres://u:p@127.0.0.1:1/db")
	if runtime.Run(ctx, false) == nil {
		t.Fatal("unavailable database accepted")
	}
}
