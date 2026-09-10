package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	if _, e := New("http://bad/path", "b", "a", "s", false); e == nil {
		t.Fatal("bad endpoint")
	}
	exists := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(204)
			return
		}
		if r.Method == "HEAD" && !exists {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if strings.Contains(r.URL.RawQuery, "location") {
				_, _ = io.WriteString(w, `<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`)
				return
			}
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
			_, _ = io.WriteString(w, "body")
		}
	}))
	defer server.Close()
	s, e := New(strings.TrimPrefix(server.URL, "http://"), "bucket", "access", "secret", false)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if e = s.Health(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Put(ctx, "key", strings.NewReader("body"), 4, "text/plain"); e != nil {
		t.Fatal(e)
	}
	r, e := s.Get(ctx, "key")
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(r)
	_ = r.Close()
	if e != nil || string(b) != "body" {
		t.Fatalf("get %s %v", b, e)
	}
	if e = s.Delete(ctx, "key"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PresignGet(ctx, "key", time.Minute); e != nil {
		t.Fatal(e)
	}
	if _, e = s.PresignGet(ctx, "key", 0); e == nil {
		t.Fatal("invalid ttl")
	}
	exists = false
	if s.Health(ctx) == nil {
		t.Fatal("missing bucket")
	}
	server.Close()
	if s.Health(ctx) == nil {
		t.Fatal("missing server")
	}
}
