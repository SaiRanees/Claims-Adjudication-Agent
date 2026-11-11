// Command api serves the claims REST API defined in api/openapi.yaml.
//
// This is a stub until week 1, session 4, which replaces it: load the
// configuration, open the Postgres pool, mount the generated handler
// (internal/api/apigen) on an http.ServeMux, and shut down cleanly on SIGINT
// or SIGTERM.
package main

import (
	"log/slog"
	"os"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	logger.Error("api is a stub until week 1, session 4")
	os.Exit(1)
}
