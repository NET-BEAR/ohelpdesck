package runtime

import (
	"context"
	"errors"
	"fmt"
	"github.com/krassus/ohelpdesck/internal/platform/config"
	"github.com/krassus/ohelpdesck/internal/platform/database"
	"github.com/krassus/ohelpdesck/internal/platform/httpserver"
	"github.com/krassus/ohelpdesck/internal/platform/logging"
	"github.com/krassus/ohelpdesck/internal/platform/redis"
	"github.com/krassus/ohelpdesck/internal/platform/storage"
	"github.com/krassus/ohelpdesck/internal/platform/telemetry"
	"net/http"
	"os"
	"time"
)

func Run(ctx context.Context, worker bool) error {
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
	defer func() {
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
	defer db.Close()
	cache, e := redis.Open(c.RedisURL)
	if e != nil {
		return e
	}
	defer func() {
		if cache.Close() != nil {
			log.Error("redis close failed")
		}
	}()
	store, e := storage.New(c.S3Endpoint, c.S3Bucket, c.S3AccessKey, c.S3SecretKey, c.S3UseSSL)
	if e != nil {
		return e
	}
	metrics := telemetry.NewMetrics(func() float64 { return float64(db.Stat().AcquiredConns()) }, func() float64 { return float64(db.Stat().IdleConns()) })
	servers := []*http.Server{{Addr: c.MetricsAddress, Handler: metrics.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, IdleTimeout: c.IdleTimeout}}
	if !worker {
		servers = append(servers, &http.Server{Addr: c.HTTPAddress, Handler: httpserver.New(map[string]httpserver.Check{"postgres": db.Ping, "redis": cache.Health, "object_storage": store.Health}, c.CORSOrigins, metrics, log), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: c.ReadTimeout, WriteTimeout: c.WriteTimeout, IdleTimeout: c.IdleTimeout})
	}
	log.Info("starting")
	errs := make(chan error, len(servers))
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
	for _, s := range servers {
		if err := s.Shutdown(shutdown); err != nil {
			_ = s.Close()
			e = fmt.Errorf("HTTP shutdown timed out")
		}
	}
	return e
}
