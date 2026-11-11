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
	LogLevel slog.Level
}

// LoadAPIConfig reads the configuration through getenv, which is os.Getenv outside tests.
// A missing required variable is an error that names every one that is missing.
func LoadAPIConfig(getenv func(string) string) (APIConfig, error) {
	var missing []string
	required := func(name string) string {
		v := getenv(name)
		if v == "" {
			missing = append(missing, name)
		}
		return v
	}
	cfg := APIConfig{
		DatabaseURL: required("DATABASE_URL"),
		JWTSecret:   []byte(required("AUTH_JWT_SECRET")),
		HTTPAddr:    getenv("HTTP_ADDR"),
	}
	if len(missing) > 0 {
		return APIConfig{}, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}
	if cfg.HTTPAddr == "" {
		cfg.HTTPAddr = ":8080"
	}
	if level := getenv("LOG_LEVEL"); level != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(level)); err != nil {
			return APIConfig{}, errors.Join(fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", level), err)
		}
	}
	return cfg, nil
}
