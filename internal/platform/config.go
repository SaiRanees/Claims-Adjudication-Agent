package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// APIConfig is the API's configuration, read from the environment (compose.yaml sets it
// for the api container; .env.example lists the values).
type APIConfig struct {
	// DatabaseURL connects as claims_app, the role with only the grants the services need.
	DatabaseURL string
	// JWTSecret verifies bearer tokens (AUTH_JWT_SECRET). NewAuthenticator checks its length.
	JWTSecret []byte
	// HTTPAddr is the listen address. Default ":8080".
	HTTPAddr string
	// LogLevel is debug, info, warn or error. Default info.
	LogLevel  slog.Level
	Telemetry TelemetryConfig
}

// RelayConfig is the relay's configuration, read from the environment as compose.yaml
// sets it for the relay container.
type RelayConfig struct {
	// DatabaseURL connects as claims_app, which reads and updates the outbox.
	DatabaseURL string
	Temporal    TemporalConfig
	// LogLevel is debug, info, warn or error. Default info.
	LogLevel  slog.Level
	Telemetry TelemetryConfig
}

// WorkerConfig is the worker's configuration, read from the environment as compose.yaml
// sets it for the worker container. The placeholder workflow needs no database yet.
type WorkerConfig struct {
	Temporal TemporalConfig
	// LogLevel is debug, info, warn or error. Default info.
	LogLevel  slog.Level
	Telemetry TelemetryConfig
}

// TemporalConfig says where Temporal is. Both values are required: the claims namespace
// is not Temporal's default, so a silent default would talk to the wrong one.
type TemporalConfig struct {
	// Address is host:port (TEMPORAL_ADDRESS).
	Address string
	// Namespace is TEMPORAL_NAMESPACE.
	Namespace string
}

// TelemetryConfig says where traces go. Only OTLP over HTTP (http/protobuf) is supported.
type TelemetryConfig struct {
	// ServiceName is OTEL_SERVICE_NAME, defaulting to the service's own name.
	ServiceName string
	// OTLPEndpoint is OTEL_EXPORTER_OTLP_ENDPOINT, the collector's base URL. Empty means
	// spans are still created, so logs and outbox rows carry trace IDs, but not exported.
	OTLPEndpoint string
}

// LoadAPIConfig reads the configuration through getenv, which is os.Getenv outside tests.
// A missing required variable is an error that names every one that is missing.
func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	env := environment{getenv: getenv}
	cfg := APIConfig{
		DatabaseURL: env.required("DATABASE_URL"),
		JWTSecret:   []byte(env.required("AUTH_JWT_SECRET")),
		HTTPAddr:    getenv("HTTP_ADDR"),
	}
	if err := env.err(); err != nil {
		return APIConfig{}, err
	}
	var err error
	if cfg.LogLevel, err = logLevel(getenv); err != nil {
		return APIConfig{}, err
	}
	if cfg.Telemetry, err = telemetry(getenv, "api"); err != nil {
		return APIConfig{}, err
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	return cfg, nil
}

// LoadRelayConfig reads the relay's configuration as LoadAPIConfig reads the API's.
func LoadRelayConfig(getenv func(string) string) (RelayConfig, error) {
	env := environment{getenv: getenv}
	cfg := RelayConfig{
		DatabaseURL: env.required("DATABASE_URL"),
		Temporal:    env.temporal(),
	}
	if err := env.err(); err != nil {
		return RelayConfig{}, err
	}
	var err error
	if cfg.LogLevel, err = logLevel(getenv); err != nil {
		return RelayConfig{}, err
	}
	if cfg.Telemetry, err = telemetry(getenv, "relay"); err != nil {
		return RelayConfig{}, err
	}
	return cfg, nil
}

// LoadWorkerConfig reads the worker's configuration as LoadAPIConfig reads the API's.
func LoadWorkerConfig(getenv func(string) string) (WorkerConfig, error) {
	env := environment{getenv: getenv}
	cfg := WorkerConfig{Temporal: env.temporal()}
	if err := env.err(); err != nil {
		return WorkerConfig{}, err
	}
	var err error
	if cfg.LogLevel, err = logLevel(getenv); err != nil {
		return WorkerConfig{}, err
	}
	if cfg.Telemetry, err = telemetry(getenv, "worker"); err != nil {
		return WorkerConfig{}, err
	}
	return cfg, nil
}

// environment reads variables and remembers which required ones were missing.
type environment struct {
	getenv  func(string) string
	missing []string
}

func (e *environment) required(name string) string {
	v := e.getenv(name)
	if v == "" {
		e.missing = append(e.missing, name)
	}
	return v
}

func (e *environment) temporal() TemporalConfig {
	return TemporalConfig{
		Address:   e.required("TEMPORAL_ADDRESS"),
		Namespace: e.required("TEMPORAL_NAMESPACE"),
	}
}

// err names every required variable that was missing.
func (e *environment) err() error {
	if len(e.missing) == 0 {
		return nil
	}
	return fmt.Errorf("missing required environment variables: %s", strings.Join(e.missing, ", "))
}

// logLevel parses LOG_LEVEL, which defaults to info.
func logLevel(getenv func(string) string) (slog.Level, error) {
	var level slog.Level
	if v := getenv("LOG_LEVEL"); v != "" {
		if err := level.UnmarshalText([]byte(v)); err != nil {
			return 0, errors.Join(fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", v), err)
		}
	}
	return level, nil
}

// telemetry reads the OTEL variables. service is the default service name.
func telemetry(getenv func(string) string, service string) (TelemetryConfig, error) {
	cfg := TelemetryConfig{ServiceName: getenv("OTEL_SERVICE_NAME"), OTLPEndpoint: getenv("OTEL_EXPORTER_OTLP_ENDPOINT")}
	if cfg.ServiceName == "" {
		cfg.ServiceName = service
	}
	if p := getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); p != "" && p != "http/protobuf" {
		return TelemetryConfig{}, fmt.Errorf("OTEL_EXPORTER_OTLP_PROTOCOL must be http/protobuf, got %q", p)
	}
	return cfg, nil
}
