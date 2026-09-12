package redis

import (
	"context"
	"net"
	"testing"
	"time"
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
func TestContextTimeout(t *testing.T) {
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	release := make(chan struct{})
	defer close(release)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		<-release
	}()
	c, e := Open("redis://" + listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if c.Health(ctx) == nil {
		t.Fatal("silent peer accepted")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("Redis ignored context deadline")
	}
}
