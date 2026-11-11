// Command api serves the claims REST API defined in api/openapi.yaml.
//
// It reads its configuration from the environment (platform.LoadAPIConfig), connects to
// Postgres as claims_app, and serves the generated handler behind the contract checks
// in internal/api. On SIGINT or SIGTERM it reports unhealthy, then lets in-flight
// requests finish before it exits.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// shutdownGrace is how long in-flight requests get to finish after a stop signal.
const shutdownGrace = 15 * time.Second

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := platform.LoadAPIConfig(os.Getenv)
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

	auth, err := platform.NewAuthenticator(cfg.JWTSecret, time.Now)
	if err != nil {
		return err
	}
	srv := api.NewServer(claims.NewStore(pool), log)
	handler, err := api.NewHandler(srv, auth, log)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// The claim events stream (server-sent events) will need its own deadline when it
		// is built; every operation served today answers well within this.
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  2 * time.Minute,
		ErrorLog:     slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.ListenAndServe() }()
	log.Info("api listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serving HTTP: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	srv.Drain()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving HTTP: %w", err)
	}
	log.Info("stopped")
	return nil
}
