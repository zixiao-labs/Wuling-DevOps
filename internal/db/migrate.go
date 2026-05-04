package db

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// migrationsFS contains the SQL files that define the schema.
//
// Files are named NNNN_name.up.sql / NNNN_name.down.sql. NNNN is a zero-padded
// integer applied in ascending order. Down migrations exist but are not run by
// the API server — the wuling-migrate CLI exposes them.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migration is a single ordered schema change.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// LoadMigrations enumerates and pairs the embedded SQL files.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	type pair struct {
		up, down string
		name     string
	}
	byVer := map[int]*pair{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// expected: NNNN_some_name.{up,down}.sql
		base := strings.TrimSuffix(name, ".sql")
		var direction string
		switch {
		case strings.HasSuffix(base, ".up"):
			base = strings.TrimSuffix(base, ".up")
			direction = "up"
		case strings.HasSuffix(base, ".down"):
			base = strings.TrimSuffix(base, ".down")
			direction = "down"
		default:
			return nil, fmt.Errorf("unexpected migration filename %q", name)
		}
		parts := strings.SplitN(base, "_", 2)
		if len(parts) < 2 {
			return nil, fmt.Errorf("migration %q must be NNNN_name", name)
		}
		ver, err := strconv.Atoi(parts[0])
		if err != nil {
			return nil, fmt.Errorf("migration %q: bad version: %w", name, err)
		}
		raw, err := fs.ReadFile(migrationsFS, path.Join("migrations", name))
		if err != nil {
			return nil, err
		}
		p, ok := byVer[ver]
		if !ok {
			p = &pair{name: parts[1]}
			byVer[ver] = p
		}
		switch direction {
		case "up":
			p.up = string(raw)
		case "down":
			p.down = string(raw)
		}
	}

	versions := make([]int, 0, len(byVer))
	for v := range byVer {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	out := make([]Migration, 0, len(versions))
	for _, v := range versions {
		p := byVer[v]
		if p.up == "" {
			return nil, fmt.Errorf("migration %d (%s): missing .up.sql", v, p.name)
		}
		out = append(out, Migration{Version: v, Name: p.name, Up: p.up, Down: p.down})
	}
	return out, nil
}

// MigrateUp applies any unapplied migrations in order. Each migration runs in
// its own transaction so a failure leaves earlier ones committed.
func MigrateUp(ctx context.Context, pool *Pool, log *slog.Logger) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}
		log.Info("applying migration", "version", m.Version, "name", m.Name)
		if err := runInTx(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.Up); err != nil {
				return fmt.Errorf("exec up: %w", err)
			}
			_, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
				m.Version, m.Name)
			return err
		}); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
	}
	return nil
}

// MigrateDown reverts the last applied migration if any.
func MigrateDown(ctx context.Context, pool *Pool, log *slog.Logger) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}
	// Find the highest applied version.
	last := -1
	for v := range applied {
		if v > last {
			last = v
		}
	}
	if last < 0 {
		log.Info("no migrations to revert")
		return nil
	}
	var target *Migration
	for i := range migrations {
		if migrations[i].Version == last {
			target = &migrations[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("applied migration %d not found in embed", last)
	}
	if target.Down == "" {
		return fmt.Errorf("migration %d (%s) has no down", target.Version, target.Name)
	}
	log.Info("reverting migration", "version", target.Version, "name", target.Name)
	return runInTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, target.Down); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, target.Version)
		return err
	})
}

// MigrationStatus reports applied/pending versions.
type MigrationStatus struct {
	Applied []int
	Pending []int
}

func Status(ctx context.Context, pool *Pool) (*MigrationStatus, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return nil, err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return nil, err
	}
	st := &MigrationStatus{}
	for _, m := range migrations {
		if applied[m.Version] {
			st.Applied = append(st.Applied, m.Version)
		} else {
			st.Pending = append(st.Pending, m.Version)
		}
	}
	return st, nil
}

func ensureMigrationsTable(ctx context.Context, pool *Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`)
	return err
}

func appliedVersions(ctx context.Context, pool *Pool) (map[int]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func runInTx(ctx context.Context, pool *Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
