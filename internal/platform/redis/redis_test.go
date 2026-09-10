package redis

import (
	"context"
	"testing"
)

func TestRedis(t *testing.T) {
	if _, e := Open("://"); e == nil {
		t.Fatal("invalid accepted")
	}
	c, e := Open("redis://127.0.0.1:1")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if c.Health(context.Background()) == nil {
		t.Fatal("expected unavailable")
	}
}
