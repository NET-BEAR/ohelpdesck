package main

import "testing"

func TestMigrateRequiresDirection(t *testing.T) {
	if err := run(nil); err == nil || err.Error() != "usage: migrate up|down|status" {
		t.Fatalf("unexpected migrate failure: %v", err)
	}
}

func TestMigrateDoesNotExposeDatabaseInput(t *testing.T) {
	t.Setenv("DATABASE_URL", "://password-sentinel")
	if err := run([]string{"up"}); err == nil || err.Error() != "database unavailable" {
		t.Fatalf("unsafe migration failure: %v", err)
	}
}
