package platform_test

import (
	"log/slog"
	"maps"
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
	if want := (platform.TelemetryConfig{ServiceName: "api"}); cfg.Telemetry != want {
		t.Errorf("Telemetry = %+v, want %+v", cfg.Telemetry, want)
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

func TestLoadRelayConfig(t *testing.T) {
	full := map[string]string{
		"DATABASE_URL":                "postgres://claims_app@db:5432/claims",
		"TEMPORAL_ADDRESS":            "temporal:7233",
		"TEMPORAL_NAMESPACE":          "claims",
		"LOG_LEVEL":                   "debug",
		"OTEL_SERVICE_NAME":           "relay-1",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-lgtm:4318",
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
	}
	tests := []struct {
		name     string
		vars     map[string]string
		want     platform.RelayConfig
		errNames []string // each must appear in the error; nil means no error
	}{
		{
			name: "reads every variable",
			vars: full,
			want: platform.RelayConfig{
				DatabaseURL: "postgres://claims_app@db:5432/claims",
				Temporal:    platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				LogLevel:    slog.LevelDebug,
				Telemetry:   platform.TelemetryConfig{ServiceName: "relay-1", OTLPEndpoint: "http://otel-lgtm:4318"},
			},
		},
		{
			name: "defaults the log level to info",
			vars: unsetVar(full, "LOG_LEVEL"),
			want: platform.RelayConfig{
				DatabaseURL: "postgres://claims_app@db:5432/claims",
				Temporal:    platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				LogLevel:    slog.LevelInfo,
				Telemetry:   platform.TelemetryConfig{ServiceName: "relay-1", OTLPEndpoint: "http://otel-lgtm:4318"},
			},
		},
		{
			name: "defaults the service name and exports nothing without an endpoint",
			vars: unsetVar(unsetVar(full, "OTEL_SERVICE_NAME"), "OTEL_EXPORTER_OTLP_ENDPOINT"),
			want: platform.RelayConfig{
				DatabaseURL: "postgres://claims_app@db:5432/claims",
				Temporal:    platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				LogLevel:    slog.LevelDebug,
				Telemetry:   platform.TelemetryConfig{ServiceName: "relay"},
			},
		},
		{
			name:     "rejects an OTLP protocol other than http/protobuf",
			vars:     setVar(full, "OTEL_EXPORTER_OTLP_PROTOCOL", "grpc"),
			errNames: []string{"OTEL_EXPORTER_OTLP_PROTOCOL"},
		},
		{
			name:     "names every missing variable",
			vars:     nil,
			errNames: []string{"DATABASE_URL", "TEMPORAL_ADDRESS", "TEMPORAL_NAMESPACE"},
		},
		{
			name:     "rejects an unknown log level",
			vars:     setVar(full, "LOG_LEVEL", "loud"),
			errNames: []string{"LOG_LEVEL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := platform.LoadRelayConfig(env(tt.vars))
			checkErr(t, err, tt.errNames)
			if err == nil && got != tt.want {
				t.Errorf("LoadRelayConfig = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadWorkerConfig(t *testing.T) {
	full := map[string]string{
		"TEMPORAL_ADDRESS":   "temporal:7233",
		"TEMPORAL_NAMESPACE": "claims",
		"LOG_LEVEL":          "warn",
	}
	tests := []struct {
		name     string
		vars     map[string]string
		want     platform.WorkerConfig
		errNames []string
	}{
		{
			name: "reads every variable",
			vars: full,
			want: platform.WorkerConfig{
				Temporal:  platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				LogLevel:  slog.LevelWarn,
				Telemetry: platform.TelemetryConfig{ServiceName: "worker"},
			},
		},
		{
			name: "needs no database",
			vars: unsetVar(full, "LOG_LEVEL"),
			want: platform.WorkerConfig{
				Temporal:  platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				LogLevel:  slog.LevelInfo,
				Telemetry: platform.TelemetryConfig{ServiceName: "worker"},
			},
		},
		{
			name:     "names every missing variable",
			vars:     nil,
			errNames: []string{"TEMPORAL_ADDRESS", "TEMPORAL_NAMESPACE"},
		},
		{
			name:     "rejects an unknown log level",
			vars:     setVar(full, "LOG_LEVEL", "loud"),
			errNames: []string{"LOG_LEVEL"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := platform.LoadWorkerConfig(env(tt.vars))
			checkErr(t, err, tt.errNames)
			if err == nil && got != tt.want {
				t.Errorf("LoadWorkerConfig = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// checkErr fails unless err is nil when want is empty, or names everything in want.
func checkErr(t *testing.T, err error, want []string) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("no error, want one naming %v", want)
	}
	for _, name := range want {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q doesn't name %s", err, name)
		}
	}
}

func unsetVar(vars map[string]string, name string) map[string]string {
	out := maps.Clone(vars)
	delete(out, name)
	return out
}

func setVar(vars map[string]string, name, value string) map[string]string {
	out := maps.Clone(vars)
	out[name] = value
	return out
}
