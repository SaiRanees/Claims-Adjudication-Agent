package documents_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// s3Config reads the TEST_S3_* variables, which point at a SeaweedFS with its bucket made:
// the local stack's (dev test sets them from .env) or CI's Database job's. Without them
// the test skips, unless TEST_S3_REQUIRED is set, as in CI's Database job, where a skip
// would let the S3 tests look done without running.
func s3Config(t *testing.T) platform.S3Config {
	t.Helper()
	cfg := platform.S3Config{
		Endpoint:        os.Getenv("TEST_S3_ENDPOINT"),
		Region:          "us-east-1",
		Bucket:          os.Getenv("TEST_S3_BUCKET"),
		AccessKeyID:     os.Getenv("TEST_S3_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("TEST_S3_SECRET_KEY"),
	}
	cfg.PublicEndpoint = cfg.Endpoint
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		const msg = "TEST_S3_ENDPOINT, TEST_S3_BUCKET, TEST_S3_ACCESS_KEY and TEST_S3_SECRET_KEY aren't all set; this test needs SeaweedFS"
		if os.Getenv("TEST_S3_REQUIRED") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	return cfg
}

func objects(t *testing.T) *documents.Objects {
	t.Helper()
	o := documents.NewObjects(s3Config(t))
	if err := o.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	return o
}

func TestPresignedPutSignsOnlyTheLengthTypeAndHost(t *testing.T) {
	o := documents.NewObjects(platform.S3Config{
		Endpoint: "http://seaweedfs:8333", PublicEndpoint: "http://localhost:8333", Region: "us-east-1",
		Bucket: "claim-documents", AccessKeyID: "test-key", SecretAccessKey: "test-secret",
	})
	before := time.Now()
	up, err := o.PresignPut(context.Background(), "quarantine/c/f", "application/pdf", 1234, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(up.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "localhost:8333" || u.Path != "/claim-documents/quarantine/c/f" {
		t.Errorf("URL = %s, want the public endpoint and a path-style key", up.URL)
	}
	q := u.Query()
	if got := q.Get("X-Amz-SignedHeaders"); got != "content-length;content-type;host" {
		t.Errorf("signed headers = %q, want content-length;content-type;host", got)
	}
	for name := range q {
		if lower := strings.ToLower(name); strings.HasPrefix(lower, "x-amz-checksum") || strings.HasPrefix(lower, "x-amz-sdk-checksum") {
			t.Errorf("the URL carries a checksum parameter, %s, that a browser can't compute", name)
		}
	}
	if q.Get("X-Amz-Expires") != "900" {
		t.Errorf("X-Amz-Expires = %q, want 900", q.Get("X-Amz-Expires"))
	}
	signed, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
	if err != nil {
		t.Fatal(err)
	}
	if !up.ExpiresAt.Equal(signed.Add(15*time.Minute)) || up.ExpiresAt.Before(before.Add(15*time.Minute-time.Second)) {
		t.Errorf("ExpiresAt = %v, signed at %v", up.ExpiresAt, signed)
	}
	if len(up.Headers) != 1 || up.Headers["Content-Type"] != "application/pdf" {
		t.Errorf("Headers = %v, want only Content-Type", up.Headers)
	}
}

// put sends body to an upload as a client would, with the upload's headers, or with
// contentType instead when it's set. chunked sends the body with no Content-Length.
func put(t *testing.T, up documents.Upload, body []byte, contentType string, chunked bool) int {
	t.Helper()
	var reader io.Reader = bytes.NewReader(body)
	if chunked {
		reader = io.NopCloser(reader) // an unknown length: net/http sends it chunked
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, up.URL, reader)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func TestPresignedPutAcceptsOnlyTheSignedLengthAndType(t *testing.T) {
	o := objects(t)
	ctx := context.Background()
	file := []byte("%PDF-1.4\n% SYNTHETIC TEST DOCUMENT\n")
	size := int64(len(file))
	tests := []struct {
		name        string
		body        []byte
		contentType string
		chunked     bool
		wantStatus  int
	}{
		{"one byte longer than signed", append(append([]byte{}, file...), 'x'), "", false, http.StatusForbidden},
		{"one byte shorter than signed", file[:size-1], "", false, http.StatusForbidden},
		{"chunked, with no length", file, "", true, 0},
		{"another content type", file, "image/png", false, http.StatusForbidden},
		{"exactly what was signed", file, "", false, http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := "test/" + uuid.NewString()
			up, err := o.PresignPut(ctx, key, "application/pdf", size, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			status := put(t, up, tc.body, tc.contentType, tc.chunked)
			if tc.wantStatus == http.StatusOK {
				if status != http.StatusOK {
					t.Fatalf("PUT = %d, want 200", status)
				}
				got, err := o.Size(ctx, key)
				if err != nil || got != size {
					t.Fatalf("Size = %d, %v; want %d", got, err, size)
				}
				rc, err := o.Open(ctx, key)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rc.Close() }()
				stored, err := io.ReadAll(rc)
				if err != nil || !bytes.Equal(stored, file) {
					t.Fatalf("stored %q, %v; want %q", stored, err, file)
				}
				return
			}
			if status >= 200 && status < 300 {
				t.Fatalf("PUT = %d, want it refused", status)
			}
			if tc.wantStatus != 0 && status != tc.wantStatus {
				t.Errorf("PUT = %d, want %d", status, tc.wantStatus)
			}
			if _, err := o.Size(ctx, key); !errors.Is(err, documents.ErrNoObject) {
				t.Errorf("after a refused PUT, Size: err = %v, want ErrNoObject", err)
			}
		})
	}
}

func TestSizeAndOpenReportAMissingObject(t *testing.T) {
	o := objects(t)
	ctx := context.Background()
	key := "test/" + uuid.NewString()
	if _, err := o.Size(ctx, key); !errors.Is(err, documents.ErrNoObject) {
		t.Errorf("Size: err = %v, want ErrNoObject", err)
	}
	if _, err := o.Open(ctx, key); !errors.Is(err, documents.ErrNoObject) {
		t.Errorf("Open: err = %v, want ErrNoObject", err)
	}
}
