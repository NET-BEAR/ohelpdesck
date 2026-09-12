package runtime

import (
	"context"
	"testing"
	"time"
)

func TestMissingConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if Run(context.Background(), false) == nil {
		t.Fatal("missing configuration")
	}
}

func TestCloseWithinCompletesOrHonorsDeadline(t *testing.T) {
	completed := make(chan struct{})
	closeWithin(time.Second, func() { close(completed) })
	select {
	case <-completed:
	default:
		t.Fatal("completed closer was not called")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	deadline := 20 * time.Millisecond
	before := time.Now()
	closeWithin(deadline, func() {
		close(started)
		<-release
	})
	if elapsed := time.Since(before); elapsed < deadline {
		t.Fatalf("closeWithin returned before deadline: %s", elapsed)
	}
	select {
	case <-started:
	default:
		t.Fatal("blocking closer did not start")
	}
	close(release)
}

func TestRunWorkerWithRegistryRequiresRegistration(t *testing.T) {
	err := RunWorkerWithRegistry(context.Background(), nil)
	if err == nil {
		t.Fatal("RunWorkerWithRegistry accepted nil registration")
	}
	if got, want := err.Error(), "worker registration is required"; got != want {
		t.Fatalf("registration error = %q, want %q", got, want)
	}
}
