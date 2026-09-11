package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/NET-BEAR/ohelpdesck/internal/jobs"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/runtime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestMissingConfig(t *testing.T) {
	if os.Getenv("TEST_ENTRYPOINT") == "1" {
		main()
		return
	}
	c := exec.Command(os.Args[0], "-test.run=TestMissingConfig")
	c.Env = append(os.Environ(), "TEST_ENTRYPOINT=1", "ENVIRONMENT=production", "DATABASE_URL=", "REDIS_URL=redis://localhost:6379", "S3_ENDPOINT=localhost:9000", "S3_BUCKET=test", "S3_ACCESS_KEY=test-access", "S3_SECRET_KEY=test-secret")
	out, e := c.CombinedOutput()
	if e == nil || !bytes.Contains(out, []byte("DATABASE_URL")) {
		t.Fatalf("expected safe missing-field message; got %s", out)
	}
}

type sigtermBlockedHandler struct {
	readyPath string
}

func (h sigtermBlockedHandler) Handle(context.Context, pgx.Tx, jobs.DomainEvent) error {
	if err := os.WriteFile(h.readyPath, []byte("started"), 0o600); err != nil {
		return err
	}
	// Deliberately exceeds the configured grace period and ignores cancellation.
	time.Sleep(5 * time.Second)
	return nil
}

func TestWorkerSIGTERMHelper(t *testing.T) {
	if os.Getenv("WORKER_SIGTERM_HELPER") != "1" {
		return
	}
	readyPath := os.Getenv("WORKER_SIGTERM_READY_PATH")
	if readyPath == "" {
		os.Exit(2)
	}
	os.Exit(runWorker(func(ctx context.Context) error {
		return runtime.RunWorkerWithRegistry(ctx, func(registry *jobs.Registry) error {
			return registry.Register(jobs.Route{EventType: "worker.sigterm", Handler: "worker.sigterm.blocked"}, sigtermBlockedHandler{readyPath: readyPath})
		})
	}))
}

func TestWorkerSIGTERMBoundedShutdownAndLeaseRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, os.Getenv("DATABASE_URL"), 8)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Migrate(ctx, "up"); err != nil {
		t.Fatal(err)
	}
	eventID := uuid.New()
	if _, err = pool.Exec(ctx, `INSERT INTO outbox_events(id,aggregate_type,aggregate_id,event_type,payload,payload_version,correlation_id,occurred_at) VALUES($1,'worker-sigterm-test',$2,'worker.sigterm','{}',1,$3,clock_timestamp())`, eventID, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM outbox_events WHERE id=$1`, eventID) })

	readyPath := filepath.Join(t.TempDir(), "handler-started")
	cmd := exec.Command(os.Args[0], "-test.run=TestWorkerSIGTERMHelper")
	cmd.Env = append(os.Environ(), "WORKER_SIGTERM_HELPER=1", "WORKER_SIGTERM_READY_PATH="+readyPath, "SHUTDOWN_TIMEOUT=100ms", "METRICS_ADDRESS=127.0.0.1:0")
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	waitReady := time.Now().Add(8 * time.Second)
	for {
		if _, statErr := os.Stat(readyPath); statErr == nil {
			break
		}
		if time.Now().After(waitReady) {
			_ = cmd.Process.Kill()
			<-exited
			t.Fatalf("blocked handler did not start: %s", output.String())
		}
		select {
		case processErr := <-exited:
			t.Fatalf("worker exited before handler start: %v %s", processErr, output.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	start := time.Now()
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case processErr := <-exited:
		if processErr != nil {
			t.Fatalf("worker SIGTERM exit=%v output=%s", processErr, output.String())
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("worker exceeded shutdown bound: %s", elapsed)
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatal("worker ignored SIGTERM")
	}
	var status jobs.Status
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='worker.sigterm.blocked'`, eventID).Scan(&status); err != nil || status != jobs.Running {
		t.Fatalf("SIGTERM job status=%s err=%v", status, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE event_id=$1 AND handler='worker.sigterm.blocked'`, eventID); err != nil {
		t.Fatal(err)
	}
	if recovered, err := jobs.NewRepository(pool).RecoverExpired(ctx, 1); err != nil || recovered != 1 {
		t.Fatalf("lease recovery=%d err=%v", recovered, err)
	}
}
