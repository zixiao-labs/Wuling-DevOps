// Command wuling-migrate is a tiny CLI for managing schema migrations.
//
// Usage:
//
//	wuling-migrate up           # apply all pending migrations
//	wuling-migrate down         # revert the last migration
//	wuling-migrate status       # show applied/pending versions
//
// Connection string and other DB settings are read from the same env vars as
// wuling-api (WULING_DB_DSN etc.), so the two binaries can share a single
// .env file.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zixiao-labs/wuling-devops/internal/applog"
	"github.com/zixiao-labs/wuling-devops/internal/config"
	"github.com/zixiao-labs/wuling-devops/internal/db"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, "usage: wuling-migrate {up|down|status}")
}

func run(cmd string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := applog.New(cfg.Log.Level, cfg.Log.Format)
	ctx := context.Background()
	pool, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch cmd {
	case "up":
		return db.MigrateUp(ctx, pool, log)
	case "down":
		return db.MigrateDown(ctx, pool, log)
	case "status":
		st, err := db.Status(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Println("applied:", st.Applied)
		fmt.Println("pending:", st.Pending)
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}
