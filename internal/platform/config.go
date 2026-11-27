package platform

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
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
	// S3 is the object store that claim documents are uploaded to.
	S3 S3Config
}

// S3Config says where the object store is (SeaweedFS in development) and how to sign in.
// Every value is required.
type S3Config struct {
	// Endpoint is S3_ENDPOINT, the URL the service calls, such as http://seaweedfs:8333.
	Endpoint string
	// PublicEndpoint is S3_PUBLIC_ENDPOINT, the URL upload links are signed for: the address
	// a client can reach, such as http://localhost:8333, not the in-network name.
	PublicEndpoint string
	// Region is S3_REGION. SeaweedFS accepts any; the signature covers it.
	Region string
	// Bucket is S3_BUCKET.
	Bucket string
	// AccessKeyID and SecretAccessKey are AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY.
	AccessKeyID     string
	SecretAccessKey string
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
// sets it for the worker container.
type WorkerConfig struct {
	// DatabaseURL connects as claims_app, which records each file's verdict.
	DatabaseURL string
	Temporal    TemporalConfig
	// S3 is where uploads wait and accepted copies go. The worker signs no URLs, so it
	// doesn't need PublicEndpoint.
	S3 S3Config
	// LogLevel is debug, info, warn or error. Default info.
	LogLevel  slog.Level
	Telemetry TelemetryConfig
	// LLMMode is LLM_MODE, and only live is accepted (ADR 0014): the worker
	// never writes a cassette from real data or replays one into a claim.
	LLMMode llm.Mode
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
		S3:          env.s3(true),
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
	cfg := WorkerConfig{
		DatabaseURL: env.required("DATABASE_URL"),
		Temporal:    env.temporal(),
		S3:          env.s3(false),
	}
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
	if cfg.LLMMode, err = workerLLMMode(getenv); err != nil {
		return WorkerConfig{}, err
	}
	return cfg, nil
}

// workerLLMMode reads LLM_MODE, default live, and refuses any other mode.
func workerLLMMode(getenv func(string) string) (llm.Mode, error) {
	v := getenv("LLM_MODE")
	if v == "" {
		return llm.ModeLive, nil
	}
	m, err := llm.ParseMode(v)
	if err == nil {
		err = llm.RequireLive(m)
	}
	if err != nil {
		return "", fmt.Errorf("LLM_MODE: %w", err)
	}
	return m, nil
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

// s3 reads the object store's settings; S3_PUBLIC_ENDPOINT only when public is set.
func (e *environment) s3(public bool) S3Config {
	cfg := S3Config{
		Endpoint:        e.required("S3_ENDPOINT"),
		Region:          e.required("S3_REGION"),
		Bucket:          e.required("S3_BUCKET"),
		AccessKeyID:     e.required("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: e.required("AWS_SECRET_ACCESS_KEY"),
	}
	if public {
		cfg.PublicEndpoint = e.required("S3_PUBLIC_ENDPOINT")
	}
	return cfg
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
