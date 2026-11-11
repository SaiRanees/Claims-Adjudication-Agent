// Command worker runs the Temporal workers: extraction, the agent and the
// rules engine.
//
// This is a stub until week 1, session 5, which replaces it with a worker that
// registers a placeholder ClaimWorkflow on the "claims" task queue.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Error("worker is a stub until week 1, session 5")
	os.Exit(1)
}
