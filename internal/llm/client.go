package llm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Mode is how a Client answers (ADR 0014).
type Mode string

// The three modes.
const (
	// ModeLive calls the provider and writes nothing.
	ModeLive Mode = "live"
	// ModeRecord replays a request that has a cassette, and calls the
	// provider only for one that doesn't, writing its cassette. A run cut
	// off by a rate limit simply runs again later and continues.
	ModeRecord Mode = "record"
	// ModeReplay reads cassettes only; a miss fails.
	ModeReplay Mode = "replay"
)

// ParseMode reads a mode's name.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(s); m {
	case ModeLive, ModeRecord, ModeReplay:
		return m, nil
	}
	return "", fmt.Errorf("llm: mode %q, want live, record or replay", s)
}

// RequireLive is the worker's check: it accepts only live, so no cassette
// is ever written from real data and none is ever replayed into a claim.
func RequireLive(m Mode) error {
	if m != ModeLive {
		return fmt.Errorf("llm: the worker runs only in live mode, not %q", m)
	}
	return nil
}

// Options configure a Client.
type Options struct {
	Mode      Mode
	Provider  Provider       // not needed in replay
	Cassettes *CassetteStore // record and replay
	Limiter   *Limiter       // live and record
	Config    Config         // prices
	// Rerecord names jobs whose cassettes a record run replaces on purpose.
	Rerecord []string
	Now      func() time.Time
}

// Client makes model calls in one mode.
type Client struct{ o Options }

// NewClient checks the options a mode needs.
func NewClient(o Options) (*Client, error) {
	if _, err := ParseMode(string(o.Mode)); err != nil {
		return nil, err
	}
	if o.Mode != ModeLive && o.Cassettes == nil {
		return nil, fmt.Errorf("llm: %s mode needs a cassette store", o.Mode)
	}
	if o.Mode != ModeReplay && (o.Provider == nil || o.Limiter == nil) {
		return nil, fmt.Errorf("llm: %s mode needs a provider and a limiter", o.Mode)
	}
	if len(o.Rerecord) > 0 && o.Mode != ModeRecord {
		return nil, errors.New("llm: only record mode re-records")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Client{o: o}, nil
}

// Result is a call's answer with its request hash and cost.
type Result struct {
	Response
	Hash       string
	CostMicros int64
	Replayed   bool // answered from a cassette
}

// Call answers one request. A failed stop is a Result, not an error: check
// Result.Failed. Errors are a cassette miss in replay, a rate limit, an
// unavailable provider, or a request the provider can't serve.
func (c *Client) Call(ctx context.Context, req Request) (Result, error) {
	sum, err := Summarize(req)
	if err != nil {
		return Result{}, err
	}
	hash, err := sum.Hash()
	if err != nil {
		return Result{}, err
	}
	rerecord := c.o.Mode == ModeRecord && slices.Contains(c.o.Rerecord, req.Job)
	if c.o.Mode != ModeLive && !rerecord {
		cas, err := c.o.Cassettes.Load(sum, hash)
		switch {
		case err == nil:
			return Result{Response: cas.Response(), Hash: hash, CostMicros: cas.CostMicros, Replayed: true}, nil
		case !errors.Is(err, ErrCassetteMiss) || c.o.Mode == ModeReplay:
			return Result{}, err
		}
	}
	p := c.o.Provider
	if p.Name() != req.Provider {
		return Result{}, fmt.Errorf("llm: request for provider %s sent to %s", req.Provider, p.Name())
	}
	if err := checkCapabilities(p.Capabilities(), req); err != nil {
		return Result{}, err
	}
	model, ok := c.o.Config.Models[req.Model]
	if !ok || model.Prices == nil {
		return Result{}, fmt.Errorf("llm: no prices configured for model %s", req.Model)
	}
	ticket, err := c.o.Limiter.Acquire(ctx, req.Model, EstimateInputTokens(req))
	if err != nil {
		return Result{}, err
	}
	resp, err := p.Generate(ctx, req)
	if err != nil {
		return Result{}, fmt.Errorf("llm: %s job %s, request %s: %w", req.Model, req.Job, hash, err)
	}
	ticket.Done(int(resp.Usage.InputTokens))
	cost, err := model.Prices.Cost(resp.Usage, false)
	if err != nil {
		return Result{}, err
	}
	res := Result{Response: resp, Hash: hash, CostMicros: cost}
	if c.o.Mode == ModeRecord {
		err := c.o.Cassettes.Save(Cassette{
			Format: CassetteFormat, Hash: hash, Request: sum,
			Output: resp.Output, Stop: resp.Stop, StopDetail: resp.StopDetail,
			Usage: resp.Usage, ModelVersion: resp.ModelVersion, CostMicros: cost,
			RecordedAt: recordedAt(c.o.Now()),
		})
		if err != nil {
			return Result{}, err
		}
	}
	return res, nil
}

// BuildRequest assembles a job's request from config: its provider, model,
// thinking level and token cap, with the given prompt, schema and parts.
func (c Config) BuildRequest(job string, prompt Prompt, schema Schema, parts []Part, sample int) (Request, error) {
	j, ok := c.Jobs[job]
	if !ok {
		return Request{}, fmt.Errorf("llm: no job %s in config", job)
	}
	if prompt.Version != j.PromptVersion || schema.Version != j.SchemaVersion {
		return Request{}, fmt.Errorf("llm: job %s is configured for prompt %s and schema %s, got %s and %s",
			job, j.PromptVersion, j.SchemaVersion, prompt.Version, schema.Version)
	}
	r := Request{
		Job: job, Provider: j.Provider, Model: j.Model, Prompt: prompt, Schema: schema,
		MaxOutputTokens: j.MaxOutputTokens, Thinking: j.Thinking, Effort: j.Effort,
		Parts: parts, Sample: sample,
	}
	return r, r.Validate()
}
