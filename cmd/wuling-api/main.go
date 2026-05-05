// Command wuling-api is the core API server for Stage 1.
//
// It runs migrations on boot (idempotent), opens the Postgres pool, brings up
// libgit2, and serves HTTP — both the JSON API at /api/v1 and Git smart HTTP
// at the root. Pipelines run as a separate process and aren't part of this
// binary.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zixiao-labs/wuling-devops/internal/applog"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/server"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
)

func main() {
	if err := run(); err != nil {
		// We may not have a logger yet — print to stderr.
		_, _ = os.Stderr.WriteString("fatal: " + err.Error() + "\n")
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := applog.New(cfg.Log.Level, cfg.Log.Format)
	slog.SetDefault(log)
	log.Info("starting wuling-api",
		"env", cfg.Env,
		"addr", cfg.HTTP.Addr,
		"repo_root", cfg.Storage.RepoRoot,
	)

	// Ensure repo root exists before serving.
	if err := os.MkdirAll(cfg.Storage.RepoRoot, 0o755); err != nil {
		return err
	}

	rootCtx, stopSignals := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	pool, err := db.Open(rootCtx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.MigrateUp(rootCtx, pool, log.With("component", "migrate")); err != nil {
		return err
	}

	if err := git.Init(); err != nil {
		return err
	}
	defer func() { _ = git.Shutdown() }()

	store := userstore.New(pool)
	layout := repostore.New(cfg.Storage.RepoRoot)

	handler := server.New(server.Deps{
		Cfg:    cfg,
		Log:    log,
		Pool:   pool,
		Store:  store,
		Layout: layout,
	})

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	// Run the server until we're signalled to stop.
	srvErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	select {
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	case err := <-srvErr:
		return err
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("graceful shutdown failed", "err", err)
		return err
	}
	log.Info("bye")
	_ = time.Now() // keep import; harmless
	return nil
}
