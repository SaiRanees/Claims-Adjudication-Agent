package platform_test

import (
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// apiEnv is every variable the API requires, plus extra.
func apiEnv(extra map[string]string) func(string) string {
	vars := map[string]string{
		"DATABASE_URL":          "postgres://db/claims",
		"AUTH_JWT_SECRET":       "0123456789abcdef0123456789abcdef",
		"S3_ENDPOINT":           "http://seaweedfs:8333",
		"S3_PUBLIC_ENDPOINT":    "http://localhost:8333",
		"S3_REGION":             "us-east-1",
		"S3_BUCKET":             "claim-documents",
		"AWS_ACCESS_KEY_ID":     "test-key",
		"AWS_SECRET_ACCESS_KEY": "test-secret",
	}
	maps.Copy(vars, extra)
	return env(vars)
}

func TestLoadAPIConfigReadsEveryVariable(t *testing.T) {
	cfg, err := platform.LoadAPIConfig(apiEnv(map[string]string{
		"DATABASE_URL": "postgres://claims_app@db:5432/claims",
		"HTTP_ADDR":    ":9090",
		"LOG_LEVEL":    "debug",
	}))
	if err != nil {
		t.Fatalf("LoadAPIConfig: %v", err)
	}
	want := platform.S3Config{
		Endpoint: "http://seaweedfs:8333", PublicEndpoint: "http://localhost:8333", Region: "us-east-1",
		Bucket: "claim-documents", AccessKeyID: "test-key", SecretAccessKey: "test-secret",
	}
	if cfg.S3 != want {
		t.Errorf("S3 = %+v, want %+v", cfg.S3, want)
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
	cfg, err := platform.LoadAPIConfig(apiEnv(nil))
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
	for _, name := range []string{"DATABASE_URL", "AUTH_JWT_SECRET", "S3_ENDPOINT", "S3_PUBLIC_ENDPOINT",
		"S3_REGION", "S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q doesn't name %s", err, name)
		}
	}
}

func TestLoadAPIConfigRejectsUnknownLogLevel(t *testing.T) {
	_, err := platform.LoadAPIConfig(apiEnv(map[string]string{"LOG_LEVEL": "loud"}))
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
		"DATABASE_URL":          "postgres://claims_app@db:5432/claims",
		"TEMPORAL_ADDRESS":      "temporal:7233",
		"TEMPORAL_NAMESPACE":    "claims",
		"S3_ENDPOINT":           "http://seaweedfs:8333",
		"S3_REGION":             "us-east-1",
		"S3_BUCKET":             "claim-documents",
		"AWS_ACCESS_KEY_ID":     "test-key",
		"AWS_SECRET_ACCESS_KEY": "test-secret",
		"LOG_LEVEL":             "warn",
	}
	s3 := platform.S3Config{
		Endpoint: "http://seaweedfs:8333", Region: "us-east-1", Bucket: "claim-documents",
		AccessKeyID: "test-key", SecretAccessKey: "test-secret",
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
				DatabaseURL: "postgres://claims_app@db:5432/claims",
				Temporal:    platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				S3:          s3,
				LogLevel:    slog.LevelWarn,
				Telemetry:   platform.TelemetryConfig{ServiceName: "worker"},
				LLMMode:     llm.ModeLive,
			},
		},
		{
			name: "needs no public S3 endpoint and defaults the log level and model mode",
			vars: unsetVar(full, "LOG_LEVEL"),
			want: platform.WorkerConfig{
				DatabaseURL: "postgres://claims_app@db:5432/claims",
				Temporal:    platform.TemporalConfig{Address: "temporal:7233", Namespace: "claims"},
				S3:          s3,
				LogLevel:    slog.LevelInfo,
				Telemetry:   platform.TelemetryConfig{ServiceName: "worker"},
				LLMMode:     llm.ModeLive,
			},
		},
		{
			name:     "refuses record mode: the worker is live only",
			vars:     setVar(full, "LLM_MODE", "record"),
			errNames: []string{"LLM_MODE"},
		},
		{
			name:     "refuses replay mode",
			vars:     setVar(full, "LLM_MODE", "replay"),
			errNames: []string{"LLM_MODE"},
		},
		{
			name:     "refuses an unknown mode",
			vars:     setVar(full, "LLM_MODE", "mock"),
			errNames: []string{"LLM_MODE"},
		},
		{
			name: "names every missing variable",
			vars: nil,
			errNames: []string{"DATABASE_URL", "TEMPORAL_ADDRESS", "TEMPORAL_NAMESPACE", "S3_ENDPOINT",
				"S3_REGION", "S3_BUCKET", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"},
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
