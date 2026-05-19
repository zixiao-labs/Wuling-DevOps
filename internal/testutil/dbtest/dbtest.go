// Package dbtest provides a Postgres-backed test fixture using testcontainers-go.
//
// Tests call Open(t) to receive a *db.Pool against a freshly migrated database
// running in a throwaway container. The container is shared per-test-binary
// (Go's `go test ./...` builds one binary per package, so each package gets its
// own isolated container) and cleaned up by testcontainers' Ryuk reaper when
// the process exits.
//
// Tests that mutate data should call Reset(t, pool) at the start of each
// subtest to truncate user-data tables.
//
// If Docker isn't reachable, Open calls t.Skip — contributors without Docker
// can still run pure unit tests.
package dbtest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	tc "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/db"
)

var (
	once       sync.Once
	sharedPool *db.Pool
	sharedErr  error
)

// errSkip signals "Docker not available; skip the test".
var errSkip = errors.New("docker not available")

// Open returns a *db.Pool against a migrated Postgres container. The container
// is shared across all tests in the same test binary.
func Open(t *testing.T) *db.Pool {
	t.Helper()
	once.Do(boot)
	if errors.Is(sharedErr, errSkip) {
		t.Skip(sharedErr.Error())
	}
	if sharedErr != nil {
		t.Fatalf("dbtest setup: %v", sharedErr)
	}
	return sharedPool
}

// Reset truncates user-data tables so subsequent tests start from a clean
// slate. The schema (and schema_migrations bookkeeping) is preserved so we
// don't pay migration cost between tests.
func Reset(t *testing.T, pool *db.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `
		TRUNCATE TABLE
			repo_commit_index,
			user_ssh_keys,
			access_tokens,
			oauth_audit_log,
			oauth_device_codes,
			oauth_access_tokens,
			oauth_auth_codes,
			oauth_auth_requests,
			oauth_authorizations,
			oauth_clients,
			org_members,
			repos,
			projects,
			orgs,
			users
		RESTART IDENTITY CASCADE
	`); err != nil {
		t.Fatalf("dbtest reset: %v", err)
	}
}

func boot() {
	if !dockerAvailable() {
		sharedErr = errSkip
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	pgC, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("wuling_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		// The postgres image logs this readiness line twice during init: once
		// when the bootstrap server comes up to apply init scripts, and again
		// after the real listener is ready. Without WithOccurrence(2) we race
		// the init shutdown and get "connection reset by peer".
		tc.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		sharedErr = err
		return
	}
	// Container is intentionally not terminated here. The first test to call
	// Open would otherwise tear it down before later tests run. Ryuk (the
	// testcontainers reaper) cleans up on process exit.

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		sharedErr = err
		return
	}

	pool, err := db.Open(ctx, config.DBConfig{
		DSN:      dsn,
		MaxConns: 8,
		MinConns: 1,
	})
	if err != nil {
		sharedErr = err
		return
	}

	// Quiet logger — migration messages are noise during tests.
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := db.MigrateUp(ctx, pool, log); err != nil {
		pool.Close()
		sharedErr = err
		return
	}
	sharedPool = pool
}

func dockerAvailable() bool {
	if os.Getenv("DOCKER_HOST") != "" {
		return true
	}
	candidates := []string{
		"/var/run/docker.sock",
		filepath.Join(os.Getenv("HOME"), ".docker", "run", "docker.sock"),
		filepath.Join(os.Getenv("HOME"), ".colima", "default", "docker.sock"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
