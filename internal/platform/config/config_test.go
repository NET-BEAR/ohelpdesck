package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	_, e := Load(func(string) string { return "" })
	if e == nil {
		t.Fatal("missing required configuration accepted")
	}
	values := map[string]string{"DATABASE_URL": "postgres://u:secret@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "access", "S3_SECRET_KEY": "secret"}
	c, e := Load(func(k string) string { return values[k] })
	if e != nil || c.HTTPAddress != ":8080" {
		t.Fatalf("load failed %v", e)
	}
	for _, k := range []string{"ENVIRONMENT", "HTTP_READ_TIMEOUT", "S3_USE_SSL", "DATABASE_MAX_CONNECTIONS"} {
		values[k] = "invalid"
		_, e = Load(func(k string) string { return values[k] })
		if e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal("invalid config accepted or secret leaked")
		}
		delete(values, k)
	}
}

func TestValidOverrides(t *testing.T) {
	v := map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "s", "ENVIRONMENT": "production", "S3_USE_SSL": "true", "DATABASE_MAX_CONNECTIONS": "20", "HTTP_READ_TIMEOUT": "4s"}
	get := func(k string) string { return v[k] }
	c, e := Load(get)
	if e != nil || !c.S3UseSSL || c.MaxConnections != 20 {
		t.Fatal(e)
	}
	for _, k := range []string{"DATABASE_URL", "REDIS_URL"} {
		old := v[k]
		v[k] = "://"
		if _, e := Load(get); e == nil {
			t.Fatal("bad URL")
		}
		v[k] = old
	}
}
