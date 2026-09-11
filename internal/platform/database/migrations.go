package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/NET-BEAR/ohelpdesck/db"
	"github.com/jackc/pgx/v5"
)

type migration struct {
	version int
	name    string
}

var migrations = []migration{{1, "foundation"}, {2, "auth_users_rbac"}, {3, "core_outbox"}, {4, "remove_core_channel_triggers"}, {5, "outbound_idempotency"}, {6, "channel_adapters"}, {7, "channel_credentials_audit"}, {8, "channel_config_version"}, {9, "durable_jobs"}}

// Migrate applies ordered, checksummed migrations. A mismatched source is a
// deployment error, not a reason to silently rewrite an already applied schema.
func (p *Pool) Migrate(ctx context.Context, direction string) (int, error) {
	if direction != "up" && direction != "down" && direction != "status" {
		return 0, fmt.Errorf("migration direction must be up, down or status")
	}
	version := 0
	e := p.WithinTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(742476)"); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version bigint PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now(), checksum text)"); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, "ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum text"); e != nil {
			return e
		}
		applied := map[int]string{}
		rows, e := tx.Query(ctx, "SELECT version, COALESCE(checksum, '') FROM schema_migrations")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var v int
			var checksum string
			if e := rows.Scan(&v, &checksum); e != nil {
				return e
			}
			applied[v] = checksum
			if v > version {
				version = v
			}
		}
		if e := rows.Err(); e != nil {
			return e
		}
		if direction == "status" {
			return nil
		}
		if direction == "down" {
			if version == 0 {
				return nil
			}
			name := ""
			for _, candidate := range migrations {
				if candidate.version == version {
					name = candidate.name
				}
			}
			if name == "" {
				return fmt.Errorf("unknown migration version")
			}
			sql, e := db.Migrations.ReadFile(fmt.Sprintf("migrations/%06d_%s.down.sql", version, name))
			if e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, string(sql)); e != nil {
				return e
			}
			if version != 1 {
				if _, e = tx.Exec(ctx, "DELETE FROM schema_migrations WHERE version=$1", version); e != nil {
					return e
				}
				version--
			} else {
				version = 0
			}
			return nil
		}
		for _, candidate := range migrations {
			path := fmt.Sprintf("migrations/%06d_%s.up.sql", candidate.version, candidate.name)
			sql, e := db.Migrations.ReadFile(path)
			if e != nil {
				return e
			}
			sum := sha256.Sum256(sql)
			checksum := hex.EncodeToString(sum[:])
			if known, ok := applied[candidate.version]; ok {
				if known != "" && known != checksum {
					return fmt.Errorf("migration checksum mismatch")
				}
				if known == "" {
					if _, e := tx.Exec(ctx, "UPDATE schema_migrations SET checksum=$1 WHERE version=$2 AND checksum IS NULL", checksum, candidate.version); e != nil {
						return e
					}
				}
				continue
			}
			if _, e = tx.Exec(ctx, string(sql)); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, "INSERT INTO schema_migrations(version, checksum) VALUES ($1, $2)", candidate.version, checksum); e != nil {
				return e
			}
			version = candidate.version
		}
		return nil
	})
	return version, e
}
