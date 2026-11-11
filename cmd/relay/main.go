// Command relay delivers outbox rows to Temporal: it starts each claim's
// workflow (ID claim-<claim id>) and sends its signals, then marks the rows
// published (ADR-0003).
//
// This is a stub until week 1, session 5, which replaces it: claim unpublished
// rows with SELECT ... FOR UPDATE SKIP LOCKED in id order, deliver them with
// the trace context stored on each row, and back off on failure.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Error("relay is a stub until week 1, session 5")
	os.Exit(1)
}
