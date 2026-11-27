//go:build live

// The live tests call Gemini with GEMINI_API_KEY. They build only with
// -tags live, never run in CI, and run through `go run ./cmd/dev smoke`,
// which passes the key from .env without printing it.

package extraction_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm/gemini"
)

// repoRoot finds the module root, for the git-ignored usage file under out/.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}

func liveClient(t *testing.T, mode llm.Mode) *llm.Client {
	t.Helper()
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Fatal("GEMINI_API_KEY isn't set; run go run ./cmd/dev smoke")
	}
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	// The day's count is shared with every other record run on this machine.
	lim, err := llm.NewLimiter(cfg.Limits(), filepath.Join(repoRoot(t), "out", "llm-usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := gemini.New(context.Background(), key, gemini.Options{})
	if err != nil {
		t.Fatal(err)
	}
	o := llm.Options{Mode: mode, Provider: p, Limiter: lim, Config: cfg}
	if mode == llm.ModeRecord {
		o.Cassettes = llm.NewCassetteStore(smokeCassettes)
	}
	c, err := llm.NewClient(o)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestLiveSmoke makes one live call per job, writing nothing, and checks
// each answer decodes: the model, thinking level and schema are accepted.
func TestLiveSmoke(t *testing.T) {
	client := liveClient(t, llm.ModeLive)
	for _, c := range smokeCases(t) {
		t.Run(c.job, func(t *testing.T) {
			res, err := client.Call(t.Context(), c.req)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s on %s: %d input, %d output, %d thinking tokens, $%d.%06d",
				c.job, res.ModelVersion, res.Usage.InputTokens, res.Usage.OutputTokens, res.Usage.ThinkingTokens,
				res.CostMicros/1_000_000, res.CostMicros%1_000_000)
			checkAnswer(t, c, res)
		})
	}
}

// TestRecordSmoke records each job's smoke cassette if it's missing; with
// both present it calls nothing.
func TestRecordSmoke(t *testing.T) {
	client := liveClient(t, llm.ModeRecord)
	for _, c := range smokeCases(t) {
		t.Run(c.job, func(t *testing.T) {
			res, err := client.Call(t.Context(), c.req)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: request %s, replayed %v", c.job, res.Hash, res.Replayed)
			checkAnswer(t, c, res)
		})
	}
}
