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
	"github.com/zixiao-labs/wuling-devops/internal/autoscale"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/db"
	"github.com/zixiao-labs/wuling-devops/internal/git"
	"github.com/zixiao-labs/wuling-devops/internal/insightstore"
	"github.com/zixiao-labs/wuling-devops/internal/issuestore"
	"github.com/zixiao-labs/wuling-devops/internal/mrstore"
	"github.com/zixiao-labs/wuling-devops/internal/pipelinestore"
	"github.com/zixiao-labs/wuling-devops/internal/repostore"
	"github.com/zixiao-labs/wuling-devops/internal/runnerstore"
	"github.com/zixiao-labs/wuling-devops/internal/secretbox"
	"github.com/zixiao-labs/wuling-devops/internal/secretstore"
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
	// Ensure the pipeline log dir exists (job logs are appended here).
	if err := os.MkdirAll(cfg.Pipeline.LogDir, 0o755); err != nil {
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

	// Secrets master key: explicit in prod (config validation enforces it),
	// auto-generated ephemerally in dev so a fresh checkout works. An ephemeral
	// key means existing ciphertext can't be decrypted after a restart — fine
	// locally, which is why prod requires WULING_SECRETS_KEY.
	var secKey []byte
	if cfg.Secrets.Key != "" {
		secKey, err = secretbox.ParseKey(cfg.Secrets.Key)
		if err != nil {
			return err
		}
	} else {
		secKey = secretbox.GenerateKey()
		log.Warn("secrets: generated ephemeral key (set WULING_SECRETS_KEY to persist across restarts)")
	}
	box, err := secretbox.New(secKey)
	if err != nil {
		return err
	}
	secrets := secretstore.New(pool, box)
	runners := runnerstore.New(pool)
	pipelines := pipelinestore.New(pool, cfg.Pipeline.LogDir)

	handler := server.New(server.Deps{
		Cfg:       cfg,
		Log:       log,
		Pool:      pool,
		Store:     store,
		Issues:    issues,
		MRs:       mrs,
		Wikis:     wikis,
		Insights:  insights,
		Layout:    layout,
		Secrets:   secrets,
		Runners:   runners,
		Pipelines: pipelines,
	})

	srv := &http.Server{
		Addr:         cfg.HTTP.Addr,
		Handler:      handler,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
		IdleTimeout:  cfg.HTTP.IdleTimeout,
	}

	// Control-plane background loops. The stale-job reaper always runs so that
	// jobs orphaned by a dead runner get requeued even when autoscaling is off.
	go runReaper(rootCtx, pipelines, cfg.Runner.ReapAfter, log.With("component", "reaper"))

	// The autoscaler reconciles each org's ephemeral runner fleet against its
	// runner-config.yaml. Disabled via WULING_AUTOSCALER_ENABLED=false.
	if cfg.Autoscale.Enabled {
		reconciler := &autoscale.Reconciler{
			Pipelines:          pipelines,
			Runners:            runners,
			Secrets:            secrets,
			Users:              store,
			Layout:             layout,
			Log:                log.With("component", "autoscaler"),
			ConfigProject:      cfg.Runner.ConfigProject,
			ConfigRepo:         cfg.Runner.ConfigRepo,
			ServerURL:          cfg.OAuth.PublicBaseURL,
			DefaultIdleTimeout: 5 * time.Minute,
			Interval:           cfg.Autoscale.Interval,
		}
		go reconciler.Run(rootCtx)
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

	// Give each shutdown its own budget. Sharing one context would let the
	// HTTP graceful shutdown burn the whole window and starve the SSH one.
	httpShutCtx, httpCancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
	defer httpCancel()
	if err := srv.Shutdown(httpShutCtx); err != nil {
		log.Warn("graceful http shutdown failed", "err", err)
	}
	if sshServer != nil {
		sshShutCtx, sshCancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
		defer sshCancel()
		if err := sshServer.Shutdown(sshShutCtx); err != nil {
			log.Warn("graceful ssh shutdown failed", "err", err)
		}
	}
	log.Info("bye")
	_ = time.Now() // keep import; harmless
	return nil
}

// runReaper periodically requeues (or fails) jobs whose runner has gone silent.
// It runs independently of the autoscaler so orphaned jobs recover even when
// autoscaling is disabled. Stops when ctx is canceled.
func runReaper(ctx context.Context, pipelines *pipelinestore.Store, reapAfter time.Duration, log *slog.Logger) {
	interval := reapAfter / 2
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := pipelines.RequeueStaleJobs(ctx, reapAfter); err != nil {
				log.Warn("reaper failed", "err", err)
			} else if n > 0 {
				log.Info("requeued/failed stale jobs", "count", n)
			}
		}
	}
}
