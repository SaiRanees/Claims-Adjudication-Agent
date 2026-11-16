// Command relay delivers outbox rows to Temporal: it starts each claim's workflow (ID
// claim-<claim id>) and sends its signals, then marks the rows published (ADR-0003).
//
// It reads its configuration from the environment (platform.LoadRelayConfig), connects
// to Postgres as claims_app and to Temporal, and runs relay.Run. Several relays can run
// at once. On SIGINT or SIGTERM it finishes the message in hand, then exits.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.temporal.io/sdk/client"
	temporallog "go.temporal.io/sdk/log"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/relay"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

// idle is how long the relay waits before looking again when nothing is due.
const idle = time.Second

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("relay stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := platform.LoadRelayConfig(os.Getenv)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("configuring the database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("reaching the database: %w", err)
	}

	tc, err := client.DialContext(ctx, client.Options{
		HostPort:  cfg.Temporal.Address,
		Namespace: cfg.Temporal.Namespace,
		Logger:    temporallog.NewStructuredLogger(log),
	})
	if err != nil {
		return fmt.Errorf("connecting to Temporal: %w", err)
	}
	defer tc.Close()

	log.Info("relay running", "temporalNamespace", cfg.Temporal.Namespace, "taskQueue", workflow.TaskQueue)
	if err := relay.New(pool, relay.NewTemporal(tc, workflow.TaskQueue), log).Run(ctx, idle); err != nil {
		return fmt.Errorf("running the relay: %w", err)
	}
	log.Info("stopped")
	return nil
}
