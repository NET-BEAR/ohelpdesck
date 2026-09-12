package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func replaceEnvironment(base []string, overrides ...string) []string {
	env := append([]string(nil), base...)
	for _, override := range overrides {
		key, _, found := strings.Cut(override, "=")
		if !found {
			continue
		}
		filtered := env[:0]
		for _, value := range env {
			if !strings.HasPrefix(value, key+"=") {
				filtered = append(filtered, value)
			}
		}
		env = append(filtered, override)
	}
	return env
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
	readyPath, returnedPath, signalPath := os.Getenv("WORKER_SIGTERM_READY_PATH"), os.Getenv("WORKER_SIGTERM_RETURNED_PATH"), os.Getenv("WORKER_SIGTERM_SIGNAL_PATH")
	if readyPath == "" || returnedPath == "" || signalPath == "" {
		os.Exit(2)
	}
	exitCode := runWorker(func(ctx context.Context) error {
		go func() {
			<-ctx.Done()
			_ = os.WriteFile(signalPath, []byte("signal-observed"), 0o600)
		}()
		return runtime.RunWorkerWithRegistry(ctx, func(registry *jobs.Registry) error {
			return registry.Register(jobs.Route{EventType: "worker.sigterm", Handler: "worker.sigterm.blocked"}, sigtermBlockedHandler{readyPath: readyPath})
		})
	})
	if err := os.WriteFile(returnedPath, []byte("runtime-returned"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(exitCode)
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

	const shutdownGrace = 100 * time.Millisecond
	tempDir := t.TempDir()
	readyPath, returnedPath, signalPath := filepath.Join(tempDir, "handler-started"), filepath.Join(tempDir, "runtime-returned"), filepath.Join(tempDir, "signal-observed")
	cmd := exec.Command(os.Args[0], "-test.run=TestWorkerSIGTERMHelper")
	cmd.Env = replaceEnvironment(os.Environ(), "WORKER_SIGTERM_HELPER=1", "WORKER_SIGTERM_READY_PATH="+readyPath, "WORKER_SIGTERM_RETURNED_PATH="+returnedPath, "WORKER_SIGTERM_SIGNAL_PATH="+signalPath, "SHUTDOWN_TIMEOUT="+shutdownGrace.String(), "METRICS_ADDRESS=127.0.0.1:0")
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
	var claimedStatus jobs.Status
	if err = pool.QueryRow(ctx, `SELECT status FROM jobs WHERE event_id=$1 AND handler='worker.sigterm.blocked'`, eventID).Scan(&claimedStatus); err != nil || claimedStatus != jobs.Running {
		t.Fatalf("handler-ready job status=%s err=%v", claimedStatus, err)
	}
	// Keep the known running lease outside any unrelated recovery window while
	// exercising SIGTERM and the subsequent explicit recovery below.
	if _, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at=clock_timestamp()+interval '24 hours' WHERE event_id=$1 AND handler='worker.sigterm.blocked'`, eventID); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	returnedDeadline := time.After(4 * shutdownGrace)
	for {
		if _, statErr := os.Stat(returnedPath); statErr == nil {
			break
		}
		select {
		case processErr := <-exited:
			t.Fatalf("worker exited before runtime return: %v %s", processErr, output.String())
		case <-returnedDeadline:
			_, signalErr := os.Stat(signalPath)
			_ = cmd.Process.Kill()
			<-exited
			if signalErr != nil {
				t.Fatal("worker process did not observe SIGTERM")
			}
			t.Fatal("worker runtime did not return after observed SIGTERM")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if elapsed := time.Since(start); elapsed > 4*shutdownGrace {
		t.Fatalf("runtime exceeded configured shutdown bound: %s", elapsed)
	}
	// A handler is deliberately non-cooperative. Once the worker runtime has
	// returned inside grace, model the supervisor's hard-stop escalation so the
	// test cannot leave that handler able to finish and falsely complete its job.
	if killErr := cmd.Process.Kill(); killErr != nil && !errors.Is(killErr, os.ErrProcessDone) {
		t.Fatalf("hard-stop worker process: %v", killErr)
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("worker process did not terminate after hard-stop")
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
