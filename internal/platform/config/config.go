package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment, HTTPAddress, MetricsAddress, DatabaseURL, RedisURL, S3Endpoint, S3Bucket, S3AccessKey, S3SecretKey, OTLPEndpoint, CORSOrigins, AuthProvider string
	S3UseSSL                                                                                                                                                 bool
	ShutdownTimeout, ReadTimeout, WriteTimeout, IdleTimeout                                                                                                  time.Duration
	MaxConnections                                                                                                                                           int32
	ChannelCredentialsKey                                                                                                                                    []byte
	ChannelCredentialsKeyID                                                                                                                                  string
	ChannelCredentialsPreviousKeys                                                                                                                           map[string][]byte
}

func Load(get func(string) string) (Config, error) {
	c := Config{Environment: "development", HTTPAddress: ":8080", MetricsAddress: ":9090", ShutdownTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxConnections: 10, AuthProvider: "local_password"}
	for k, p := range map[string]*string{"ENVIRONMENT": &c.Environment, "HTTP_ADDRESS": &c.HTTPAddress, "METRICS_ADDRESS": &c.MetricsAddress, "DATABASE_URL": &c.DatabaseURL, "REDIS_URL": &c.RedisURL, "S3_ENDPOINT": &c.S3Endpoint, "S3_BUCKET": &c.S3Bucket, "S3_ACCESS_KEY": &c.S3AccessKey, "S3_SECRET_KEY": &c.S3SecretKey, "OTEL_EXPORTER_OTLP_ENDPOINT": &c.OTLPEndpoint, "CORS_ALLOWED_ORIGINS": &c.CORSOrigins, "AUTH_PROVIDER": &c.AuthProvider} {
		if v := get(k); v != "" {
			*p = v
		}
	}
	for k, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL, "S3_ENDPOINT": c.S3Endpoint, "S3_BUCKET": c.S3Bucket, "S3_ACCESS_KEY": c.S3AccessKey, "S3_SECRET_KEY": c.S3SecretKey} {
		if v == "" {
			return Config{}, invalid("required configuration: %s", k)
		}
	}
	switch c.Environment {
	case "development", "test", "staging", "production":
	default:
		return Config{}, invalid("invalid ENVIRONMENT")
	}
	if c.Environment == "production" && get("AUTH_PROVIDER") == "" {
		return Config{}, invalid("required configuration: AUTH_PROVIDER")
	}
	if c.AuthProvider != "local_password" {
		return Config{}, invalid("invalid AUTH_PROVIDER")
	}
	for k, p := range map[string]*time.Duration{"HTTP_READ_TIMEOUT": &c.ReadTimeout, "HTTP_WRITE_TIMEOUT": &c.WriteTimeout, "HTTP_IDLE_TIMEOUT": &c.IdleTimeout, "SHUTDOWN_TIMEOUT": &c.ShutdownTimeout} {
		if v := get(k); v != "" {
			d, e := time.ParseDuration(v)
			if e != nil || d <= 0 {
				return Config{}, invalid("invalid %s", k)
			}
			*p = d
		}
	}
	if v := get("S3_USE_SSL"); v != "" {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return Config{}, invalid("invalid S3_USE_SSL")
		}
		c.S3UseSSL = b
	}
	if v := get("DATABASE_MAX_CONNECTIONS"); v != "" {
		n, e := strconv.ParseInt(v, 10, 32)
		if e != nil || n < 1 {
			return Config{}, invalid("invalid DATABASE_MAX_CONNECTIONS")
		}
		c.MaxConnections = int32(n)
	}
	for k, v := range map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL} {
		u, e := url.Parse(v)
		if e != nil || u.Host == "" {
			return Config{}, invalid("invalid %s", k)
		}
	}
	key, e := base64.StdEncoding.DecodeString(get("CHANNEL_CREDENTIALS_AES256_KEY"))
	if e != nil || len(key) != 32 {
		return Config{}, invalid("invalid CHANNEL_CREDENTIALS_AES256_KEY")
	}
	c.ChannelCredentialsKey = key
	c.ChannelCredentialsKeyID = get("CHANNEL_CREDENTIALS_KEY_ID")
	if c.ChannelCredentialsKeyID == "" {
		c.ChannelCredentialsKeyID = "v1"
	}
	c.ChannelCredentialsPreviousKeys = map[string][]byte{}
	for _, entry := range strings.Split(get("CHANNEL_CREDENTIALS_PREVIOUS_KEYS"), ",") {
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 || parts[0] == "" {
			return Config{}, invalid("invalid CHANNEL_CREDENTIALS_PREVIOUS_KEYS")
		}
		decoded, err := base64.StdEncoding.DecodeString(parts[1])
		if err != nil || len(decoded) != 32 {
			return Config{}, invalid("invalid CHANNEL_CREDENTIALS_PREVIOUS_KEYS")
		}
		c.ChannelCredentialsPreviousKeys[parts[0]] = decoded
	}
	return c, nil
}

// Error contains only a validated configuration field name, never its value.
type Error string

func (e Error) Error() string                  { return string(e) }
func invalid(format string, args ...any) error { return Error(fmt.Sprintf(format, args...)) }
