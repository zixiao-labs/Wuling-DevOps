// Command wuling-api is the core API server for Stage 1.
//
// It runs migrations on boot (idempotent), opens the Postgres pool, brings up
// libgit2, and serves HTTP — both the JSON API at /api/v1 and Git smart HTTP
// at the root. The embedded SSH listener (Git transport over SSH) runs on a
// separate port alongside the HTTP listener. Pipelines run as a separate
// process and aren't part of this binary.
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
	"github.com/zixiao-labs/wuling-devops/internal/insightstore"
	"github.com/zixiao-labs/wuling-devops/internal/issuestore"
	"github.com/zixiao-labs/wuling-devops/internal/mrstore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/server"
	"github.com/zixiao-labs/wuling-devops/internal/sshd"
	"github.com/zixiao-labs/wuling-devops/internal/userstore"
	"github.com/zixiao-labs/wuling-devops/internal/wikistore"
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
		"ssh_enabled", cfg.SSH.Enabled,
		"ssh_addr", cfg.SSH.Addr,
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
	issues := issuestore.New(pool)
	mrs := mrstore.New(pool)
	layout := repostore.New(cfg.Storage.RepoRoot)
	wikis := wikistore.New(layout)
	insights := insightstore.New(pool, log.With("component", "insights"))

	handler := server.New(server.Deps{
		Cfg:      cfg,
		Log:      log,
		Pool:     pool,
		Store:    store,
		Issues:   issues,
		MRs:      mrs,
		Wikis:    wikis,
		Insights: insights,
		Layout:   layout,
	})

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	// Run HTTP and SSH until we're signalled to stop. Errors from either
	// goroutine collapse onto a single channel so the main loop can react
	// to whichever fails first.
	srvErr := make(chan error, 2)
	go func() {
		log.Info("listening", "addr", cfg.HTTP.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			srvErr <- err
		}
	}()

	var sshServer *sshd.Server
	if cfg.SSH.Enabled {
		sshServer, err = sshd.New(sshd.Deps{
			Cfg:     cfg.SSH,
			Log:     log.With("component", "sshd"),
			Store:   store,
			Layout:  layout,
			Indexer: insights,
		})
		if err != nil {
			return err
		}
		sshServer.Start(srvErr)
	}

	select {
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	case err := <-srvErr:
		// Best-effort shutdown of whatever is still running.
		_ = srv.Close()
		if sshServer != nil {
			_ = sshServer.Shutdown(context.Background())
		}
		return err
	}

	shutCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn("graceful http shutdown failed", "err", err)
	}
	if sshServer != nil {
		if err := sshServer.Shutdown(shutCtx); err != nil {
			log.Warn("graceful ssh shutdown failed", "err", err)
		}
	}
	log.Info("bye")
	_ = time.Now() // keep import; harmless
	return nil
}
