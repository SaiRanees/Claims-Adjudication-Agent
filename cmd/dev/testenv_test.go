package main

import (
	"maps"
	"testing"
)

const (
	plainPW = "app-dev-only" //nolint:gosec // G101: a made-up password that exists only in this test.
	oddPW   = "p@ss/w:rd"    //nolint:gosec // G101: a made-up password that exists only in this test.
)

func TestLocalTestEnv(t *testing.T) {
	const appVar = "CLAIMS_APP_PASSWORD"
	dbURL := func(escapedPW string) string {
		return "postgres://claims_app:" + escapedPW + "@localhost:5432/claims?sslmode=disable"
	}
	s3 := map[string]string{"S3_BUCKET": "claim-documents", "S3_ACCESS_KEY": "claims-dev", "S3_SECRET_KEY": "s3-secret"}
	withS3 := func(vars map[string]string) map[string]string {
		out := maps.Clone(vars)
		maps.Copy(out, s3)
		return out
	}
	tests := []struct {
		name   string
		dotenv map[string]string
		set    []string
		want   map[string]string
	}{
		{
			name:   "fills both from .env",
			dotenv: map[string]string{appVar: plainPW},
			want:   map[string]string{"TEST_DATABASE_URL": dbURL(plainPW), "TEST_TEMPORAL_ADDRESS": "localhost:7233"},
		},
		{
			name:   "fills the S3 variables from .env",
			dotenv: withS3(map[string]string{appVar: plainPW}),
			set:    []string{"TEST_DATABASE_URL", "TEST_TEMPORAL_ADDRESS"},
			want: map[string]string{
				"TEST_S3_ENDPOINT": "http://localhost:8333", "TEST_S3_BUCKET": "claim-documents",
				"TEST_S3_ACCESS_KEY": "claims-dev", "TEST_S3_SECRET_KEY": "s3-secret",
			},
		},
		{
			name:   "no S3 variables without all three S3 values",
			dotenv: map[string]string{"S3_BUCKET": "claim-documents", "S3_ACCESS_KEY": "claims-dev"},
			set:    []string{"TEST_TEMPORAL_ADDRESS"},
			want:   map[string]string{},
		},
		{
			name:   "keeps variables already set",
			dotenv: withS3(map[string]string{appVar: plainPW}),
			set:    []string{"TEST_DATABASE_URL", "TEST_TEMPORAL_ADDRESS", "TEST_S3_ENDPOINT"},
			want:   map[string]string{},
		},
		{
			name: "no database URL without a password",
			want: map[string]string{"TEST_TEMPORAL_ADDRESS": "localhost:7233"},
		},
		{
			name:   "escapes the password",
			dotenv: map[string]string{appVar: oddPW},
			set:    []string{"TEST_TEMPORAL_ADDRESS"},
			want:   map[string]string{"TEST_DATABASE_URL": dbURL("p%40ss%2Fw%3Ard")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isSet := func(name string) bool {
				for _, s := range tt.set {
					if s == name {
						return true
					}
				}
				return false
			}
			if got := localTestEnv(tt.dotenv, isSet); !maps.Equal(got, tt.want) {
				t.Errorf("localTestEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
