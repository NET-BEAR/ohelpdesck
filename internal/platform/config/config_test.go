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
	values := map[string]string{"DATABASE_URL": "postgres://u:secret@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "access", "S3_SECRET_KEY": "secret", "SESSION_SECRET": "a-session-secret-with-sufficient-length", "CHANNEL_CREDENTIALS_AES256_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}
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
	v := map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "s", "AUTH_PROVIDER": "local_password", "ENVIRONMENT": "production", "S3_USE_SSL": "true", "DATABASE_MAX_CONNECTIONS": "20", "HTTP_READ_TIMEOUT": "4s", "CHANNEL_CREDENTIALS_AES256_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}
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

func TestLocalPasswordAuthenticationConfiguration(t *testing.T) {
	v := map[string]string{
		"DATABASE_URL": "postgres://u:p@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "s",
		"AUTH_PROVIDER": "local_password", "CHANNEL_CREDENTIALS_AES256_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
	}
	get := func(k string) string { return v[k] }
	if c, err := Load(get); err != nil || c.AuthProvider != "local_password" {
		t.Fatalf("valid local auth rejected: %v", err)
	}
	v["AUTH_PROVIDER"] = "unknown"
	if _, err := Load(get); err == nil {
		t.Fatal("unknown provider accepted")
	}
	v["ENVIRONMENT"] = "production"
	delete(v, "AUTH_PROVIDER")
	if _, err := Load(get); err == nil {
		t.Fatal("production accepted implicit auth provider")
	}
}

func TestChannelCredentialKeyMustBeBase64EncodedAES256Key(t *testing.T) {
	v := map[string]string{
		"DATABASE_URL": "postgres://u:p@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "s",
		"CHANNEL_CREDENTIALS_AES256_KEY": "not-a-key",
	}
	if _, err := Load(func(k string) string { return v[k] }); err == nil || strings.Contains(err.Error(), "not-a-key") {
		t.Fatal("invalid credential key was accepted or leaked")
	}
}

func TestPreviousCredentialKeysAreDecodedWithoutExposingValues(t *testing.T) {
	v := map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db", "REDIS_URL": "redis://localhost:6379", "S3_ENDPOINT": "localhost:9000", "S3_BUCKET": "test", "S3_ACCESS_KEY": "a", "S3_SECRET_KEY": "s", "CHANNEL_CREDENTIALS_AES256_KEY": "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", "CHANNEL_CREDENTIALS_KEY_ID": "next", "CHANNEL_CREDENTIALS_PREVIOUS_KEYS": "old:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="}
	c, err := Load(func(k string) string { return v[k] })
	if err != nil || c.ChannelCredentialsKeyID != "next" || len(c.ChannelCredentialsPreviousKeys["old"]) != 32 {
		t.Fatalf("keyring config=%+v err=%v", c.ChannelCredentialsKeyID, err)
	}
}
