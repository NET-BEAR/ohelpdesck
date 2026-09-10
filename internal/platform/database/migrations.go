package database

import (
	"context"
	"fmt"
	"github.com/NET-BEAR/ohelpdesck/db"
	"github.com/jackc/pgx/v5"
)

// Migrate creates bookkeeping only; no business schema exists in SPEC-000.
func (p *Pool) Migrate(ctx context.Context, direction string) (int, error) {
	if direction != "up" && direction != "down" && direction != "status" {
		return 0, fmt.Errorf("migration direction must be up, down or status")
	}
	version := 0
	e := p.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(742476)"); e != nil {
			return e
		}
		if direction == "status" {
			var exists bool
			if e := tx.QueryRow(ctx, "SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&exists); e != nil {
				return e
			}
			if !exists {
				return nil
			}
			return tx.QueryRow(ctx, "SELECT COALESCE(MAX(version),0) FROM schema_migrations").Scan(&version)
		}
		if direction == "down" {
			sql, e := db.Migrations.ReadFile("migrations/000001_foundation.down.sql")
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, string(sql))
			return e
		}
		sql, e := db.Migrations.ReadFile("migrations/000001_foundation.up.sql")
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, string(sql))
		version = 1
		return e
	})
	return version, e
}
