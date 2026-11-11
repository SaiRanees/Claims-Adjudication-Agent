package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// TestTwoIdenticalCreatesWithOneKeyMakeOneClaim is week 1, session 4's exit check, end to
// end: the HTTP handler, the claims store and the migrated, seeded database
// (TEST_DATABASE_URL, as claims_app).
func TestTwoIdenticalCreatesWithOneKeyMakeOneClaim(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; this test needs the migrated, seeded database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	auth, err := platform.NewAuthenticator(apiSecret, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(api.NewServer(claims.NewStore(pool), log), auth, log)
	if err != nil {
		t.Fatal(err)
	}
	subject := "test-" + uuid.NewString()
	tok, err := auth.Issue(platform.Principal{Subject: subject, Role: platform.RoleSubmitter}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	first := create(t, h, tok, "acceptance-key", validBody)
	second := create(t, h, tok, "acceptance-key", validBody)

	if first.Code != http.StatusCreated || second.Code != http.StatusCreated {
		t.Fatalf("statuses = %d, %d, want 201 twice; bodies %s / %s", first.Code, second.Code, first.Body, second.Body)
	}
	if first.Body.String() != second.Body.String() {
		t.Errorf("the retry's body differs:\nfirst  %s\nsecond %s", first.Body, second.Body)
	}
	location := first.Header().Get("Location")
	if !strings.HasPrefix(location, "/v1/claims/") || second.Header().Get("Location") != location {
		t.Errorf("Location = %q then %q", location, second.Header().Get("Location"))
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM claims WHERE created_by = $1`, subject).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d claims, want 1", n)
	}

	reused := create(t, h, tok, "acceptance-key", strings.Replace(validBody, "12500000", "12500001", 1))
	problemOf(t, reused, http.StatusUnprocessableEntity, "idempotency_key_reused")

	got := do(h, call{method: http.MethodGet, path: location, auth: "Bearer " + tok})
	if got.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", location, got.Code, got.Body)
	}
	if !strings.Contains(got.Body.String(), `"status":"draft"`) {
		t.Errorf("GET %s = %s, want a draft", location, got.Body)
	}
}
