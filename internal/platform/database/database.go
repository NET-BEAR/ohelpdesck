package database

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"time"
)

type Pool struct{ *pgxpool.Pool }
type tracer struct{}

func (tracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	ctx, _ = otel.Tracer("database").Start(ctx, "postgres.query")
	return ctx
}
func (tracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	span.End()
}
func Open(ctx context.Context, dsn string, max int32) (*Pool, error) {
	c, e := pgxpool.ParseConfig(dsn)
	if e != nil {
		return nil, fmt.Errorf("invalid database configuration")
	}
	c.MaxConns = max
	c.MaxConnLifetime = time.Hour
	c.ConnConfig.ConnectTimeout = 5 * time.Second
	c.ConnConfig.Tracer = tracer{}
	p, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		return nil, fmt.Errorf("database initialization failed")
	}
	if e = p.Ping(ctx); e != nil {
		p.Close()
		return nil, fmt.Errorf("database unavailable")
	}
	return &Pool{p}, nil
}

type TxManager interface {
	WithinTx(context.Context, func(context.Context, pgx.Tx) error) error
}

func (p *Pool) WithinTx(ctx context.Context, fn func(context.Context, pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, p.Pool, func(tx pgx.Tx) error { return fn(ctx, tx) })
}
