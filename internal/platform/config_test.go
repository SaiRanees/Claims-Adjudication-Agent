package platform_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestLoadAPIConfigReadsEveryVariable(t *testing.T) {
	cfg, err := platform.LoadAPIConfig(env(map[string]string{
		"DATABASE_URL":    "postgres://claims_app@db:5432/claims",
		"AUTH_JWT_SECRET": "0123456789abcdef0123456789abcdef",
		"HTTP_ADDR":       ":9090",
		"LOG_LEVEL":       "debug",
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig: %v", err)
	}
	if cfg.DatabaseURL != "postgres://claims_app@db:5432/claims" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if string(cfg.JWTSecret) != "0123456789abcdef0123456789abcdef" {
		t.Errorf("JWTSecret = %q", cfg.JWTSecret)
	}
	if cfg.HTTPAddr != ":9090" {
		t.Errorf("HTTPAddr = %q, want :9090", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want DEBUG", cfg.LogLevel)
	}
}

func TestLoadAPIConfigDefaultsOptionalVariables(t *testing.T) {
	cfg, err := platform.LoadAPIConfig(env(map[string]string{
		"DATABASE_URL":    "postgres://db/claims",
		"AUTH_JWT_SECRET": "0123456789abcdef0123456789abcdef",
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig: %v", err)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want INFO", cfg.LogLevel)
	}
}

func TestLoadAPIConfigNamesEveryMissingVariable(t *testing.T) {
	_, err := platform.LoadAPIConfig(env(nil))
	if err == nil {
		t.Fatal("LoadAPIConfig succeeded with no environment")
	}
	for _, name := range []string{"DATABASE_URL", "AUTH_JWT_SECRET"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q doesn't name %s", err, name)
		}
	}
}

func TestLoadAPIConfigRejectsUnknownLogLevel(t *testing.T) {
	_, err := platform.LoadAPIConfig(env(map[string]string{
		"DATABASE_URL":    "postgres://db/claims",
		"AUTH_JWT_SECRET": "0123456789abcdef0123456789abcdef",
		"LOG_LEVEL":       "loud",
	}))
	if err == nil {
		t.Fatal("LoadAPIConfig accepted LOG_LEVEL=loud")
	}
}
