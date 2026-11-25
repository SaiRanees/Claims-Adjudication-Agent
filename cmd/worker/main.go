// Command worker runs the Temporal worker for the "claims" task queue. It hosts
// ClaimWorkflow and the file checks, which read uploads from the object store, open them in
// PDFium's sandbox and record each verdict in Postgres; extraction, the agent and the rules
// engine join it in later sessions.
//
// It reads its configuration from the environment (platform.LoadWorkerConfig). On SIGINT
// or SIGTERM it stops polling and gives running activities stopGrace to finish.
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
	"go.temporal.io/sdk/worker"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

// stopGrace is how long running activities get to finish after a stop signal.
const stopGrace = 15 * time.Second

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := platform.LoadWorkerConfig(os.Getenv)
	if err != nil {
		return err
	}
	log := platform.NewLogger(os.Stderr, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stopTelemetry, err := platform.StartTelemetry(ctx, cfg.Telemetry, log)
	if err != nil {
		return err
	}
	defer stopTelemetry()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("configuring the database pool: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("reaching the database: %w", err)
	}

	s3 := cfg.S3
	s3.PublicEndpoint = s3.Endpoint // the worker signs no upload URLs
	objects := documents.NewObjects(s3)
	if err := objects.Ping(ctx); err != nil {
		return err
	}

	pdfium, err := documents.NewPDFium(documents.DefaultPDFiumConfig)
	if err != nil {
		return err
	}
	defer func() { _ = pdfium.Close() }()

	tc, err := platform.DialTemporal(ctx, cfg.Temporal, log)
	if err != nil {
		return err
	}
	defer tc.Close()

	w := worker.New(tc, workflow.TaskQueue, worker.Options{WorkerStopTimeout: stopGrace})
	workflow.Register(w, &workflow.Activities{
		Checker: documents.NewChecker(claims.NewStore(pool), objects, pdfium),
	})
	if err := w.Start(); err != nil {
		return fmt.Errorf("starting the worker: %w", err)
	}
	log.Info("worker running", "temporalNamespace", cfg.Temporal.Namespace, "taskQueue", workflow.TaskQueue)

	<-ctx.Done()
	log.Info("shutting down")
	w.Stop()
	log.Info("stopped")
	return nil
}
