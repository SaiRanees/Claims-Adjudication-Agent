//go:build live

// The record run calls Gemini with GEMINI_API_KEY. It builds only with
// -tags live, never runs in CI, and runs through
// `go run ./cmd/dev record-pages`, which passes the key from .env without
// printing it.

package extraction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	jobs "github.com/SaiRanees/Claims-Adjudication-Agent/internal/extraction"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm/gemini"
)

// unavailableRetries is how many times a page is asked again after a 503,
// with a growing wait, before the run moves on and leaves it for next time.
const unavailableRetries = 3

// TestRecordClassification records page classification on the dev half,
// calling Gemini only for pages without a cassette. The client's limiter
// keeps it under the free tier's limits from models.yaml and counts the
// day's requests in out/llm-usage.json. At the day's limit, or a 429, it
// stops cleanly and reports how many pages remain; every cassette written
// stays, and the next run continues from there. Only dev-half packets are
// ever prepared, so no test-half page reaches the model.
func TestRecordClassification(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Fatal("GEMINI_API_KEY isn't set; run go run ./cmd/dev record-pages")
	}
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	lim, err := llm.NewLimiter(cfg.Limits(), filepath.Join("..", "..", "out", "llm-usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := gemini.New(context.Background(), key, gemini.Options{})
	if err != nil {
		t.Fatal(err)
	}
	client, err := llm.NewClient(llm.Options{
		Mode: llm.ModeRecord, Provider: p, Limiter: lim, Config: cfg,
		Cassettes: llm.NewCassetteStore(cassetteDir),
	})
	if err != nil {
		t.Fatal(err)
	}
	packets := preparedDevHalf(t)
	total := 0
	for _, pk := range packets {
		total += len(pk.pages)
	}
	answered, failed, skipped := 0, 0, 0
	start := time.Now()
	for _, pk := range packets {
		for _, in := range pk.pages {
			for attempt := 0; ; attempt++ {
				_, err := jobs.Classify(t.Context(), client, cfg, in)
				switch {
				case err == nil:
					answered++
				case errors.Is(err, llm.ErrDailyLimit), errors.Is(err, llm.ErrRateLimited):
					t.Logf("stopped at %s %s: %v", pk.id, in.ID, err)
					t.Logf("%d of %d dev-half pages answered (%d of them failed answers), %d left after 503s; "+
						"%d remain: run go run ./cmd/dev record-pages again after the quota resets at midnight Pacific",
						answered, total, failed, skipped, total-answered)
					return
				case errors.Is(err, llm.ErrUnavailable) && attempt < unavailableRetries:
					wait := time.Duration(attempt+1) * 20 * time.Second
					t.Logf("%s %s: %v; asking again in %s", pk.id, in.ID, err, wait)
					select {
					case <-time.After(wait):
					case <-t.Context().Done():
						t.Fatal(t.Context().Err())
					}
					continue
				case errors.Is(err, llm.ErrUnavailable):
					skipped++
					t.Logf("%s %s: still unavailable; left for the next run", pk.id, in.ID)
				default:
					// A stop or output that doesn't fit the schema: the
					// cassette is written, and the answer counts as failed,
					// as the replay will count it.
					answered++
					failed++
					t.Logf("%s %s: %v", pk.id, in.ID, err)
				}
				break
			}
		}
		t.Logf("%s done: %d of %d pages answered, %s", pk.id, answered, total, time.Since(start).Round(time.Second))
	}
	t.Logf("%d of %d dev-half pages answered (%d of them failed answers); %d remain", answered, total, failed, total-answered)
}
