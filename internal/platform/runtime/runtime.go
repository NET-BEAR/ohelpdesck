package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/NET-BEAR/ohelpdesck/internal/auth"
	"github.com/NET-BEAR/ohelpdesck/internal/channels"
	"github.com/NET-BEAR/ohelpdesck/internal/channels/telegrambot"
	"github.com/NET-BEAR/ohelpdesck/internal/core"
	"github.com/NET-BEAR/ohelpdesck/internal/jobs"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/config"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/database"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/httpserver"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/logging"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/redis"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/storage"
	"github.com/NET-BEAR/ohelpdesck/internal/platform/telemetry"
	"net/http"
	"os"
	"time"
)

// WorkerRegistration supplies durable handlers for an isolated worker runtime.
// Production passes nil and receives the built-in empty registry until handlers
// are registered by their owning integration slice.
type WorkerRegistration func(*jobs.Registry) error

func Run(ctx context.Context, worker bool) error {
	return run(ctx, worker, nil)
}

// RunWorkerWithRegistry starts the actual worker runtime with an isolated
// durable-job registry for process-level verification.
func RunWorkerWithRegistry(ctx context.Context, register WorkerRegistration) error {
	if register == nil {
		return fmt.Errorf("worker registration is required")
	}
	return run(ctx, true, register)
}

func closeWithin(timeout time.Duration, closer func()) {
	done := make(chan struct{})
	go func() {
		closer()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func run(ctx context.Context, worker bool, register WorkerRegistration) error {
	c, e := config.Load(os.Getenv)
	if e != nil {
		return e
	}
	service := "support-api"
	if worker {
		service = "support-worker"
	}
	log := logging.New(os.Stdout, service, c.Environment)
	stop := telemetry.Init(service, c.OTLPEndpoint)
	telemetryStopped := false
	defer func() {
		if telemetryStopped {
			return
		}
		shutdown, cancel := context.WithTimeout(context.Background(), c.ShutdownTimeout)
		defer cancel()
		if stop(shutdown) != nil {
			log.Error("telemetry shutdown failed")
		}
	}()
	connect, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	db, e := database.Open(connect, c.DatabaseURL, c.MaxConnections)
	if e != nil {
		return e
	}
	// A handler may be blocked in a database transaction while ignoring its
	// cancellation context. Pool.Close waits for that checkout, so normal
	// shutdown closes it concurrently with other bounded cleanup operations.
	databaseClosed := false
	defer func() {
		if !databaseClosed {
			closeWithin(c.ShutdownTimeout, db.Close)
		}
	}()
	cache, e := redis.Open(c.RedisURL)
	if e != nil {
		return e
	}
	cacheClosed := false
	defer func() {
		if cacheClosed {
			return
		}
		closeWithin(c.ShutdownTimeout, func() {
			if cache.Close() != nil {
				log.Error("redis close failed")
			}
		})
	}()
	store, e := storage.New(c.S3Endpoint, c.S3Bucket, c.S3AccessKey, c.S3SecretKey, c.S3UseSSL)
	if e != nil {
		return e
	}
	metrics := telemetry.NewMetrics(func() float64 { return float64(db.Stat().AcquiredConns()) }, func() float64 { return float64(db.Stat().IdleConns()) }, jobs.NewMetricsReader(db))
	servers := []*http.Server{{Addr: c.MetricsAddress, Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, IdleTimeout: c.IdleTimeout}}
	var workerLoop *jobs.Worker
	if worker {
		host, hostErr := os.Hostname()
		if hostErr != nil {
			host = "worker"
		}
		registry := jobs.NewRegistry()
		if register != nil {
			if err := register(registry); err != nil {
				return err
			}
		}
		workerLoop, e = jobs.NewWorker(jobs.NewDispatcher(db, registry), jobs.NewRepository(db), registry, jobs.WorkerConfig{WorkerID: fmt.Sprintf("%s:%d", host, os.Getpid()), PollInterval: time.Second, LeaseDuration: time.Minute, DispatchBatch: 32, RecoveryBatch: 32})
		if e != nil {
			return e
		}
	}
	if !worker {
		repository := auth.NewRepository(db)
		credentialCipher, err := channels.NewKeyring(c.ChannelCredentialsKeyID, c.ChannelCredentialsKey, c.ChannelCredentialsPreviousKeys)
		if err != nil {
			return err
		}
		registry := channels.NewRegistry()
		telegram := telegrambot.New(nil)
		registry.Register(channels.TypeTelegramBot, telegram)
		channelService := channels.NewServiceWithRegistry(db, credentialCipher, registry)
		inbound := core.NewReceiveInboundService(db)
		api := auth.NewOperatorOutboundChannelsHTTPHandler(repository, c.Environment == "production", core.NewConversationService(db), core.NewOutboundService(db), channelService, auth.Observability{Metrics: metrics, Log: log})
		mux := http.NewServeMux()
		mux.Handle("/api/v1/webhooks/telegram-bot/", telegrambot.NewWebhookHandler(telegram, channelService, channelService, inbound))
		mux.Handle("/", api)
		servers = append(servers, &http.Server{Addr: c.HTTPAddress, Handler: httpserver.NewApplication(map[string]httpserver.Check{"postgres": db.Ping, "redis": cache.Health, "object_storage": store.Health}, c.CORSOrigins, metrics, mux, log), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, IdleTimeout: c.IdleTimeout})
	}
	log.Info("starting")
	errs := make(chan error, len(servers)+1)
	if workerLoop != nil {
		go func() { errs <- workerLoop.Run(ctx) }()
	}
	for _, s := range servers {
		go func(s *http.Server) { errs <- s.ListenAndServe() }(s)
	}
	select {
	case <-ctx.Done():
	case e = <-errs:
		if errors.Is(e, http.ErrServerClosed) {
			e = nil
		}
	}
	shutdown, end := context.WithTimeout(context.Background(), c.ShutdownTimeout)
	defer end()
	// Drain HTTP first: active API handlers may still need PostgreSQL, Redis or
	// telemetry until their request has finished or the original deadline ends.
	for _, s := range servers {
		if err := s.Shutdown(shutdown); err != nil {
			_ = s.Close()
			e = fmt.Errorf("HTTP shutdown timed out")
		}
	}
	// Dependency cleanup may not extend the original shutdown deadline.
	databaseDone := make(chan struct{})
	go func() {
		db.Close()
		close(databaseDone)
	}()
	cacheDone := make(chan struct{})
	go func() {
		if cache.Close() != nil {
			log.Error("redis close failed")
		}
		close(cacheDone)
	}()
	telemetryDone := make(chan error, 1)
	go func() { telemetryDone <- stop(shutdown) }()
	select {
	case <-databaseDone:
	case <-shutdown.Done():
	}
	databaseClosed = true
	select {
	case <-cacheDone:
	case <-shutdown.Done():
	}
	cacheClosed = true
	select {
	case telemetryErr := <-telemetryDone:
		if telemetryErr != nil {
			log.Error("telemetry shutdown failed")
		}
	case <-shutdown.Done():
	}
	telemetryStopped = true
	return e
}
