package llm

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"

	"go.yaml.in/yaml/v3"
)

//go:embed models.yaml
var modelsYAML []byte

// Config is which model does each job, and each model's limits and prices.
type Config struct {
	Jobs   map[string]JobConfig   `yaml:"jobs"`
	Models map[string]ModelConfig `yaml:"models"`
}

// JobConfig sets one job's model and parameters.
type JobConfig struct {
	Provider        string `yaml:"provider"`
	Model           string `yaml:"model"`
	PromptVersion   string `yaml:"prompt_version"`
	SchemaVersion   string `yaml:"schema_version"`
	Thinking        string `yaml:"thinking"`
	Effort          string `yaml:"effort"`
	MaxOutputTokens int    `yaml:"max_output_tokens"`
}

// ModelConfig is one model's tier, limits and prices.
type ModelConfig struct {
	Provider string  `yaml:"provider"`
	Tier     string  `yaml:"tier"` // free or paid
	Limits   Limits  `yaml:"limits"`
	Prices   *Prices `yaml:"prices"`
}

// Prices are US dollar micros per million tokens. Every figure is written
// out per model, never derived by a multiplier; a nil field is a config
// error, not a zero.
type Prices struct {
	Input       *int64      `yaml:"input"`
	Output      *int64      `yaml:"output"` // thinking tokens are billed as output
	CacheWrite  *int64      `yaml:"cache_write"`
	CacheRead   *int64      `yaml:"cache_read"`
	BatchInput  *int64      `yaml:"batch_input"`
	BatchOutput *int64      `yaml:"batch_output"`
	LongPrompt  *LongPrompt `yaml:"long_prompt"` // a higher tier above a prompt size, where a model has one
}

// LongPrompt is a model's prices for prompts over AboveTokens.
type LongPrompt struct {
	AboveTokens int64  `yaml:"above_tokens"`
	Input       *int64 `yaml:"input"`
	Output      *int64 `yaml:"output"`
	CacheRead   *int64 `yaml:"cache_read"`
}

// DefaultConfig parses the embedded models.yaml.
func DefaultConfig() (Config, error) { return ParseConfig(modelsYAML) }

// ParseConfig decodes a config strictly and checks it.
func ParseConfig(src []byte) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return Config{}, fmt.Errorf("llm: config: %w", err)
	}
	if len(c.Jobs) == 0 || len(c.Models) == 0 {
		return Config{}, errors.New("llm: config: no jobs or no models")
	}
	for _, name := range slices.Sorted(maps.Keys(c.Models)) {
		m := c.Models[name]
		if err := m.check(); err != nil {
			return Config{}, fmt.Errorf("llm: config: model %s: %w", name, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Jobs)) {
		j := c.Jobs[name]
		m, ok := c.Models[j.Model]
		switch {
		case !namePattern.MatchString(name):
			return Config{}, fmt.Errorf("llm: config: job name %q", name)
		case !ok:
			return Config{}, fmt.Errorf("llm: config: job %s uses unknown model %s", name, j.Model)
		case m.Provider != j.Provider:
			return Config{}, fmt.Errorf("llm: config: job %s names provider %s, model %s is %s", name, j.Provider, j.Model, m.Provider)
		case j.PromptVersion == "" || j.SchemaVersion == "":
			return Config{}, fmt.Errorf("llm: config: job %s needs a prompt and a schema version", name)
		case j.MaxOutputTokens <= 0:
			return Config{}, fmt.Errorf("llm: config: job %s needs a positive token cap", name)
		}
	}
	return c, nil
}

func (m ModelConfig) check() error {
	if m.Provider == "" {
		return errors.New("no provider")
	}
	if m.Tier != "free" && m.Tier != "paid" {
		return fmt.Errorf("tier %q, want free or paid", m.Tier)
	}
	if m.Limits.RequestsPerMinute < 0 || m.Limits.InputTokensPerMinute < 0 || m.Limits.RequestsPerDay < 0 {
		return errors.New("negative limit")
	}
	if m.Tier == "free" && (m.Limits.RequestsPerMinute == 0 || m.Limits.RequestsPerDay == 0) {
		return errors.New("a free-tier model needs its requests a minute and a day")
	}
	p := m.Prices
	if p == nil {
		return errors.New("no prices")
	}
	figures := []*int64{p.Input, p.Output, p.CacheWrite, p.CacheRead, p.BatchInput, p.BatchOutput}
	if lp := p.LongPrompt; lp != nil {
		if lp.AboveTokens <= 0 {
			return errors.New("long_prompt needs above_tokens")
		}
		figures = append(figures, lp.Input, lp.Output, lp.CacheRead)
	}
	for _, f := range figures {
		if f == nil {
			return errors.New("every price must be written out")
		}
		if *f < 0 {
			return errors.New("negative price")
		}
	}
	if m.Tier == "free" {
		for _, f := range figures {
			if *f != 0 {
				return errors.New("a free-tier model costs $0")
			}
		}
	}
	return nil
}

// Limits returns every model's limits, for a Limiter.
func (c Config) Limits() map[string]Limits {
	out := map[string]Limits{}
	for name, m := range c.Models {
		out[name] = m.Limits
	}
	return out
}

// Cost returns a call's cost in US dollar micros, each component rounded
// half up. Batch is for calls made through a provider's batch API.
func (p Prices) Cost(u Usage, batch bool) (int64, error) {
	if u.InputTokens < 0 || u.CachedInputTokens < 0 || u.OutputTokens < 0 || u.ThinkingTokens < 0 ||
		u.CachedInputTokens > u.InputTokens {
		return 0, fmt.Errorf("llm: impossible usage %+v", u)
	}
	in, out, read := *p.Input, *p.Output, *p.CacheRead
	if batch {
		in, out = *p.BatchInput, *p.BatchOutput
	}
	if lp := p.LongPrompt; lp != nil && u.InputTokens > lp.AboveTokens && !batch {
		in, out, read = *lp.Input, *lp.Output, *lp.CacheRead
	}
	var total int64
	for _, c := range []struct{ tokens, price int64 }{
		{u.InputTokens - u.CachedInputTokens, in},
		{u.CachedInputTokens, read},
		{u.OutputTokens + u.ThinkingTokens, out},
	} {
		if c.price != 0 && c.tokens > (math.MaxInt64-500_000)/c.price {
			return 0, errors.New("llm: cost overflows")
		}
		total += (c.tokens*c.price + 500_000) / 1_000_000
	}
	return total, nil
}
