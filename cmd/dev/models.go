package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// modelKeyVar is the model provider's key (ADR 0016). It lives only in .env,
// which git ignores, and reaches only the live and record runs this file
// starts: never `dev test`, never CI, and never a log line.
const modelKeyVar = "GEMINI_API_KEY"

// smokeRun is the go test command line for the live smoke tests: record any
// missing smoke cassette, then make one live call per job.
var smokeRun = []string{
	"test", "-tags", "live", "-count=1", "-v",
	"-run", "^(TestRecordSmoke|TestLiveSmoke)$",
	"./internal/extraction/",
}

// smoke runs the live smoke tests with the key from .env.
func smoke(ctx context.Context, _ []string) error {
	if err := useModelKey(); err != nil {
		return err
	}
	return run(ctx, nil, "go", smokeRun...)
}

// useModelKey puts the key from .env into this process's environment, so
// the go test it starts inherits it. The key is never printed: run prints
// only the command line. A key already in the environment is used as it is.
func useModelKey() error {
	if os.Getenv(modelKeyVar) != "" {
		return nil
	}
	content, err := os.ReadFile(".env")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	key := parseEnv(string(content))[modelKeyVar]
	if key == "" {
		return fmt.Errorf("set %s in .env: a Google AI Studio key on a project with no billing (ADR 0016)", modelKeyVar)
	}
	return os.Setenv(modelKeyVar, key)
}
