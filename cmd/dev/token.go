package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// tokenTTL is how long a development token lasts: a working day.
const tokenTTL = 12 * time.Hour

func token(_ context.Context, args []string) error {
	if err := ensureEnv(); err != nil {
		return err
	}
	return mintToken(".env", args, os.Stdout, time.Now)
}

// mintToken prints a bearer token signed with the AUTH_JWT_SECRET in envFile, for the role
// and subject in args: [role] [subject], by default submitter and dev-<role>.
func mintToken(envFile string, args []string, out io.Writer, now func() time.Time) error {
	if len(args) > 2 {
		return errors.New("usage: go run ./cmd/dev token [submitter|reviewer|admin] [subject]")
	}
	role := platform.RoleSubmitter
	if len(args) > 0 {
		role = platform.Role(args[0])
	}
	subject := "dev-" + string(role)
	if len(args) > 1 {
		subject = args[1]
	}

	env, err := readEnvFile(envFile)
	if err != nil {
		return err
	}
	secret := env["AUTH_JWT_SECRET"]
	if secret == "" {
		return fmt.Errorf("%s has no AUTH_JWT_SECRET; copy that line from .env.example", envFile)
	}
	auth, err := platform.NewAuthenticator([]byte(secret), now)
	if err != nil {
		return err
	}
	signed, err := auth.Issue(platform.Principal{Subject: subject, Role: role}, tokenTTL)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, signed)
	return err
}

// readEnvFile reads an env file; see parseEnv.
func readEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseEnv(string(data)), nil
}

// parseEnv reads KEY=VALUE lines, skipping blank lines and # comments, as Docker Compose
// does for the simple values .env.example uses.
func parseEnv(content string) map[string]string {
	vars := map[string]string{}
	for _, line := range strings.Split(toLF(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			vars[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return vars
}
