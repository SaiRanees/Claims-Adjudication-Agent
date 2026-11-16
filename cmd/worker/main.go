// Command worker runs the Temporal worker for the "claims" task queue. For now it hosts
// the placeholder ClaimWorkflow and its one activity; extraction, the agent and the rules
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

	"go.temporal.io/sdk/worker"

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

	tc, err := platform.DialTemporal(ctx, cfg.Temporal, log)
	if err != nil {
		return err
	}
	defer tc.Close()

	w := worker.New(tc, workflow.TaskQueue, worker.Options{WorkerStopTimeout: stopGrace})
	workflow.Register(w, &workflow.Activities{})
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
