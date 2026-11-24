package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// Where the local stack publishes Postgres, Temporal and SeaweedFS's S3 API on
// the host (compose.yaml).
const (
	localPostgres = "localhost:5432"
	localTemporal = "localhost:7233"
	localS3       = "localhost:8333"
)

// localTestEnv returns the variables that point the database, Temporal and S3
// tests at the local stack: TEST_DATABASE_URL (the claims database as
// claims_app, with the password from .env), TEST_TEMPORAL_ADDRESS, and the
// four TEST_S3_* variables (SeaweedFS, with the bucket and keys from .env). A
// variable that is already set, even to an empty value, is left alone, so
// setting one to "" still skips those tests; TEST_S3_ENDPOINT stands for all
// four S3 variables.
func localTestEnv(dotenv map[string]string, isSet func(string) bool) map[string]string {
	vars := map[string]string{}
	if pw := dotenv["CLAIMS_APP_PASSWORD"]; pw != "" && !isSet("TEST_DATABASE_URL") {
		u := url.URL{
			Scheme:   "postgres",
			User:     url.UserPassword("claims_app", pw),
			Host:     localPostgres,
			Path:     "/claims",
			RawQuery: "sslmode=disable",
		}
		vars["TEST_DATABASE_URL"] = u.String()
	}
	if !isSet("TEST_TEMPORAL_ADDRESS") {
		vars["TEST_TEMPORAL_ADDRESS"] = localTemporal
	}
	bucket, key, secret := dotenv["S3_BUCKET"], dotenv["S3_ACCESS_KEY"], dotenv["S3_SECRET_KEY"]
	if bucket != "" && key != "" && secret != "" && !isSet("TEST_S3_ENDPOINT") {
		vars["TEST_S3_ENDPOINT"] = "http://" + localS3
		vars["TEST_S3_BUCKET"] = bucket
		vars["TEST_S3_ACCESS_KEY"] = key
		vars["TEST_S3_SECRET_KEY"] = secret
	}
	return vars
}

// testAddress is where the service a test variable points at must answer.
func testAddress(name string) string {
	switch {
	case name == "TEST_DATABASE_URL":
		return localPostgres
	case name == "TEST_TEMPORAL_ADDRESS":
		return localTemporal
	case strings.HasPrefix(name, "TEST_S3_"):
		return localS3
	}
	return ""
}

// useLocalStack sets the local-stack test variables in this process, so the
// go test it runs inherits them. A variable whose service isn't answering is
// left unset, with a note, so the tests that need it skip themselves rather
// than fail when the stack is down.
func useLocalStack(ctx context.Context) error {
	content, err := os.ReadFile(".env")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	vars := localTestEnv(parseEnv(string(content)), func(name string) bool {
		_, ok := os.LookupEnv(name)
		return ok
	})
	if len(vars) == 0 {
		return nil
	}
	// The relay tests empty the shared outbox's backlog inside a transaction,
	// and the Temporal test runs its own worker on the claims task queue, so a
	// relay or worker container would race them and they'd fail for no reason
	// of their own. CI never runs those containers.
	if running := runningAppServices(ctx); len(running) > 0 {
		fmt.Fprintf(os.Stderr, "note: dev app is running (%s), which would race the database and Temporal tests, so those skip themselves. Run docker compose stop %s to include them.\n",
			strings.Join(running, ", "), strings.Join(running, " "))
		return nil
	}
	// Group the variables by the address that must answer for them.
	byAddress := map[string][]string{}
	for name := range vars {
		addr := testAddress(name)
		byAddress[addr] = append(byAddress[addr], name)
	}
	addresses := make([]string, 0, len(byAddress))
	for addr := range byAddress {
		addresses = append(addresses, addr)
	}
	slices.Sort(addresses)
	for _, addr := range addresses {
		names := byAddress[addr]
		slices.Sort(names)
		list := strings.Join(names, ", ")
		if !answers(ctx, addr) {
			fmt.Fprintf(os.Stderr, "note: nothing answers on %s, so the tests that need %s skip themselves. Run dev up first to include them.\n", addr, list)
			continue
		}
		for _, name := range names {
			if err := os.Setenv(name, vars[name]); err != nil {
				return err
			}
		}
		fmt.Fprintf(os.Stderr, "note: %s set for the local stack (%s).\n", list, addr)
	}
	return nil
}

// runningAppServices returns which of the relay and worker containers are
// running. Without Docker it returns none.
func runningAppServices(ctx context.Context) []string {
	out, err := exec.CommandContext(ctx, "docker", "compose", "ps", "--status", "running", "--services").Output()
	if err != nil {
		return nil
	}
	var running []string
	for _, svc := range strings.Fields(string(out)) {
		if svc == "relay" || svc == "worker" {
			running = append(running, svc)
		}
	}
	slices.Sort(running)
	return running
}

// answers reports whether something accepts a TCP connection at addr.
func answers(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
