package llm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeClock advances only when the limiter sleeps.
type fakeClock struct {
	t      time.Time
	slept  []time.Duration
	failAt int // fail the nth sleep with a cancelled context, if > 0
}

func (c *fakeClock) now() time.Time { return c.t }
func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	if c.failAt > 0 && len(c.slept) == c.failAt {
		return context.Canceled
	}
	c.t = c.t.Add(d)
	return nil
}

func newTestLimiter(t *testing.T, limits map[string]Limits, path string, clock *fakeClock) *Limiter {
	t.Helper()
	l, err := NewLimiter(limits, path)
	if err != nil {
		t.Fatal(err)
	}
	l.now, l.sleep = clock.now, clock.sleep
	return l
}

func TestLimiterRequestsPerMinute(t *testing.T) {
	ctx := context.Background()
	// 10:00 UTC on 27 November is 02:00 that day in Los Angeles.
	clock := &fakeClock{t: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	l := newTestLimiter(t, map[string]Limits{"m": {RequestsPerMinute: 5, RequestsPerDay: 20}}, "", clock)
	for i := range 5 {
		if _, err := l.Acquire(ctx, "m", 100); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if len(clock.slept) != 0 {
		t.Fatalf("slept %v within the minute's five", clock.slept)
	}
	if _, err := l.Acquire(ctx, "m", 100); err != nil {
		t.Fatal(err)
	}
	if len(clock.slept) != 1 || clock.slept[0] != window {
		t.Errorf("the sixth request slept %v, want one wait of %v", clock.slept, window)
	}
}

func TestLimiterTokensPerMinute(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)}
	l := newTestLimiter(t, map[string]Limits{"m": {RequestsPerMinute: 100, InputTokensPerMinute: 1000, RequestsPerDay: 100}}, "", clock)
	tk, err := l.Acquire(ctx, "m", 600)
	if err != nil {
		t.Fatal(err)
	}
	tk.Done(300) // the provider counted fewer than estimated
	if _, err := l.Acquire(ctx, "m", 600); err != nil || len(clock.slept) != 0 {
		t.Fatalf("after Done(300), 600 more should fit 1000: slept %v, %v", clock.slept, err)
	}
	if _, err := l.Acquire(ctx, "m", 600); err != nil || len(clock.slept) != 1 {
		t.Errorf("900 used, 600 more should wait: slept %v, %v", clock.slept, err)
	}
	if _, err := l.Acquire(ctx, "m", 1001); err == nil {
		t.Error("one request over the minute's tokens: no error")
	}
}

func TestLimiterDailyLimitPersistsAndResets(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usage.json")
	limits := map[string]Limits{"m": {RequestsPerMinute: 100, RequestsPerDay: 3}}
	// 07:59 UTC on 28 November is 23:59 on 27 November in Los Angeles.
	clock := &fakeClock{t: time.Date(2026, 11, 28, 7, 59, 0, 0, time.UTC)}
	l := newTestLimiter(t, limits, path, clock)
	for range 2 {
		if _, err := l.Acquire(ctx, "m", 1); err != nil {
			t.Fatal(err)
		}
	}
	// A second process on the same Pacific day starts from the file.
	l2 := newTestLimiter(t, limits, path, clock)
	if l2.Used("m") != 2 {
		t.Fatalf("a new limiter sees %d requests today, want 2", l2.Used("m"))
	}
	if _, err := l2.Acquire(ctx, "m", 1); err != nil {
		t.Fatal(err)
	}
	_, err := l2.Acquire(ctx, "m", 1)
	if !errors.Is(err, ErrDailyLimit) || !strings.Contains(err.Error(), "2026-11-27") {
		t.Fatalf("the fourth request of the day: %v", err)
	}
	if len(clock.slept) != 0 {
		t.Errorf("the daily limit waited %v instead of failing", clock.slept)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"date": "2026-11-27"`) || !strings.Contains(string(b), `"m": 3`) {
		t.Errorf("usage file:\n%s", b)
	}
	// Midnight Pacific: the count starts again.
	clock.t = time.Date(2026, 11, 28, 8, 0, 0, 0, time.UTC)
	if _, err := l2.Acquire(ctx, "m", 1); err != nil {
		t.Errorf("after midnight Pacific: %v", err)
	}
	if l2.Used("m") != 1 {
		t.Errorf("new day's count %d, want 1", l2.Used("m"))
	}
}

func TestLimiterRefuses(t *testing.T) {
	ctx := context.Background()
	clock := &fakeClock{t: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), failAt: 1}
	l := newTestLimiter(t, map[string]Limits{"m": {RequestsPerMinute: 1, RequestsPerDay: 10}}, "", clock)
	if _, err := l.Acquire(ctx, "other", 1); err == nil {
		t.Error("a model with no limits: no error")
	}
	if _, err := l.Acquire(ctx, "m", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Acquire(ctx, "m", 1); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled wait: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "usage.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLimiter(nil, bad); err == nil {
		t.Error("a corrupt usage file: no error")
	}
}

func TestEstimateInputTokens(t *testing.T) {
	r := pinnedRequest()
	n := EstimateInputTokens(r)
	// Two derived pages at 1,500 each, plus the text.
	if n < 3000 || n > 4000 {
		t.Errorf("estimate %d", n)
	}
}
