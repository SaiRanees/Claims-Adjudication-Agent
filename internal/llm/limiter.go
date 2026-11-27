package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"
	_ "time/tzdata" // Google's daily quota resets at midnight Pacific time, on every OS.
)

// Limits is one model's free-tier rate limits (ADR 0016). Zero means none.
type Limits struct {
	RequestsPerMinute    int `yaml:"requests_per_minute"`
	InputTokensPerMinute int `yaml:"input_tokens_per_minute"`
	RequestsPerDay       int `yaml:"requests_per_day"`
}

// quotaZone is where Google's daily quotas reset.
const quotaZone = "America/Los_Angeles"

// window is the span the per-minute limits count over, with a second's
// margin so a request never lands on the provider's edge.
const window = 61 * time.Second

// Limiter keeps each model under its limits on our side. Per-minute use is
// counted in memory; the day's requests are counted in a file keyed by the
// Pacific date, so a second run on the same day doesn't start from zero.
// One process at a time uses a usage file.
type Limiter struct {
	mu     sync.Mutex
	limits map[string]Limits
	path   string
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	zone   *time.Location
	recent map[string][]event
	usage  dayUsage
}

type event struct {
	at     time.Time
	tokens int
}

// dayUsage is the usage file: the requests sent to each model on one Pacific
// date. A request counts whether or not it succeeds, as Google counts it.
type dayUsage struct {
	Date     string         `json:"date"` // YYYY-MM-DD in America/Los_Angeles
	Requests map[string]int `json:"requests"`
}

// NewLimiter returns a limiter for these models, reading the day's usage from
// path if it exists. An empty path keeps the day's count in memory only.
func NewLimiter(limits map[string]Limits, path string) (*Limiter, error) {
	zone, err := time.LoadLocation(quotaZone)
	if err != nil {
		return nil, err
	}
	l := &Limiter{
		limits: limits, path: path, now: time.Now, sleep: sleepCtx, zone: zone,
		recent: map[string][]event{}, usage: dayUsage{Requests: map[string]int{}},
	}
	if path == "" {
		return l, nil
	}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return l, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &l.usage); err != nil {
		return nil, fmt.Errorf("llm: usage file %s: %w", path, err)
	}
	if l.usage.Requests == nil {
		l.usage.Requests = map[string]int{}
	}
	return l, nil
}

// Ticket is one admitted request. Done records its actual input tokens.
type Ticket struct {
	l     *Limiter
	model string
	at    time.Time
}

// Acquire waits until a request to model fits its per-minute limits, then
// counts it. It fails with ErrDailyLimit when the day's requests are used,
// without waiting for the next day: a record run stops and resumes later.
func (l *Limiter) Acquire(ctx context.Context, model string, estimatedTokens int) (*Ticket, error) {
	lim, ok := l.limits[model]
	if !ok {
		return nil, fmt.Errorf("llm: no rate limits configured for model %s", model)
	}
	if lim.InputTokensPerMinute > 0 && estimatedTokens > lim.InputTokensPerMinute {
		return nil, fmt.Errorf("llm: about %d input tokens is over %s's %d a minute", estimatedTokens, model, lim.InputTokensPerMinute)
	}
	for {
		l.mu.Lock()
		now := l.now()
		date := now.In(l.zone).Format(time.DateOnly)
		if l.usage.Date != date {
			l.usage = dayUsage{Date: date, Requests: map[string]int{}}
		}
		if lim.RequestsPerDay > 0 && l.usage.Requests[model] >= lim.RequestsPerDay {
			l.mu.Unlock()
			return nil, fmt.Errorf("%w: %d of %d requests to %s on %s (Pacific); run again after midnight Pacific",
				ErrDailyLimit, l.usage.Requests[model], lim.RequestsPerDay, model, date)
		}
		recent := l.prune(model, now)
		wait := time.Duration(0)
		tokens := 0
		for _, e := range recent {
			tokens += e.tokens
		}
		if len(recent) > 0 &&
			((lim.RequestsPerMinute > 0 && len(recent) >= lim.RequestsPerMinute) ||
				(lim.InputTokensPerMinute > 0 && tokens+estimatedTokens > lim.InputTokensPerMinute)) {
			wait = recent[0].at.Add(window).Sub(now)
		}
		if wait <= 0 {
			l.recent[model] = append(recent, event{at: now, tokens: estimatedTokens})
			l.usage.Requests[model]++
			err := l.persist()
			l.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return &Ticket{l: l, model: model, at: now}, nil
		}
		l.mu.Unlock()
		if err := l.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// Done replaces the ticket's estimate with the tokens the provider counted.
func (t *Ticket) Done(inputTokens int) {
	t.l.mu.Lock()
	defer t.l.mu.Unlock()
	for i, e := range t.l.recent[t.model] {
		if e.at.Equal(t.at) {
			t.l.recent[t.model][i].tokens = inputTokens
			return
		}
	}
}

// Used returns the requests counted for model today (Pacific).
func (l *Limiter) Used(model string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.usage.Date != l.now().In(l.zone).Format(time.DateOnly) {
		return 0
	}
	return l.usage.Requests[model]
}

func (l *Limiter) prune(model string, now time.Time) []event {
	rs := l.recent[model]
	i := 0
	for i < len(rs) && now.Sub(rs[i].at) >= window {
		i++
	}
	rs = rs[i:]
	l.recent[model] = rs
	return rs
}

func (l *Limiter) persist() error {
	if l.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(l.usage, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(l.path, append(b, '\n'))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// EstimateInputTokens is a deliberately high estimate of a request's input
// tokens, used only to pace requests before the provider counts them: a
// token per three bytes of text, and 1,500 per page of a file part (whole
// files count as one page per 100 KB, at least one).
func EstimateInputTokens(r Request) int {
	n := len(r.Prompt.Text)/3 + len(r.Schema.JSON)/3
	for _, p := range r.Parts {
		if p.MIMEType == MIMEText {
			n += len(p.Data) / 3
			continue
		}
		pages := 0
		if p.Origin != nil {
			pages = len(p.Origin.Pages)
		}
		if pages == 0 {
			pages = max(1, len(p.Data)/100_000)
		}
		n += 1500 * pages
	}
	return n
}
