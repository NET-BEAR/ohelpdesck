package database

import (
	"context"
	"testing"
)

func TestInvalid(t *testing.T) {
	for _, dsn := range []string{"://", "postgres://u:p@127.0.0.1:1/db"} {
		if p, e := Open(context.Background(), dsn, 2); e == nil {
			p.Close()
			t.Fatal("expected failure")
		}
	}
}
