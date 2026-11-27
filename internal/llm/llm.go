package llm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// Provider is one model provider's adapter. Generate makes exactly one call:
// no retries, no fallbacks to another model. A stop other than StopEnd comes
// back as a Response, not an error, so it can be recorded like any other
// answer (ADR 0014); errors are for calls that produced no answer.
type Provider interface {
	Name() string
	Capabilities() Capabilities
	Generate(ctx context.Context, req Request) (Response, error)
}

// Capabilities declares what an adapter supports, so a request it can't
// serve fails before any call (architecture.md, "Swappable model providers").
type Capabilities struct {
	PDFInput         bool
	ImageInput       bool
	JSONSchemaOutput bool
	PromptCaching    bool
	Citations        bool
	StrictToolInputs bool
	// ThinkingLevels lists the thinking settings the adapter can send.
	// Which ones a given model accepts is the model's business; config
	// sets one per job (ADR 0016).
	ThinkingLevels []string
}

// Request holds only what any provider can be asked for. Everything in it
// except the bytes of file parts is covered by the request hash.
type Request struct {
	Job             string
	Provider        string
	Model           string
	Prompt          Prompt
	Schema          Schema
	MaxOutputTokens int
	Thinking        string
	Effort          string // empty where the provider has no separate effort
	Parts           []Part
	// Sample is 0 unless a step asks the same question more than once, as
	// two-pass agreement does. It joins the hash and is never sent.
	Sample int
}

// Prompt is a job's system prompt, versioned per provider.
type Prompt struct {
	ID      string // the job's prompt name, usually the job
	Version string // "v1"
	Text    []byte
}

// Schema is a job's output schema, a JSON Schema in the subset CheckSchema
// accepts.
type Schema struct {
	ID      string // e.g. "line_items"
	Version string // e.g. "v1"
	JSON    []byte
}

// MIME types a part may carry.
const (
	MIMEText = "text/plain"
	MIMEPDF  = "application/pdf"
	MIMEPNG  = "image/png"
	MIMEJPEG = "image/jpeg"
)

// Part is one piece of content sent to the model. A text part is identified
// by the SHA-256 of its bytes; any other part by its Origin, never by its
// bytes, because a derived file's bytes change on every run (ADRs 0013 and
// 0014).
type Part struct {
	MIMEType string
	Data     []byte
	Origin   *Origin
}

// Origin identifies a file part: the uploaded file's SHA-256, and for a
// derived file (a page, a page range, a photo's PDF) the pages taken and a
// derivation version bumped by hand when the way it's derived changes.
type Origin struct {
	FileSHA256 string `json:"file_sha256"`
	Pages      []int  `json:"pages"`
	Derivation int    `json:"derivation"`
}

// TextPart returns a text part.
func TextPart(s string) Part { return Part{MIMEType: MIMEText, Data: []byte(s)} }

// FilePart returns an uploaded file sent as it is, identified by its SHA-256.
func FilePart(mimeType string, data []byte) Part {
	sum := sha256.Sum256(data)
	return Part{MIMEType: mimeType, Data: data, Origin: &Origin{FileSHA256: hex.EncodeToString(sum[:])}}
}

// StopReason says why the model stopped. Only StopEnd is a usable answer.
type StopReason string

// The stop reasons, normalized across providers.
const (
	StopEnd        StopReason = "end"
	StopMaxTokens  StopReason = "max_tokens"
	StopSafety     StopReason = "safety"
	StopRecitation StopReason = "recitation"
	StopBlocked    StopReason = "blocked" // the prompt itself was refused
	StopOther      StopReason = "other"
)

// Usage counts tokens. OutputTokens excludes ThinkingTokens; both are billed
// as output.
type Usage struct {
	InputTokens       int64 `json:"input_tokens"`
	CachedInputTokens int64 `json:"cached_input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	ThinkingTokens    int64 `json:"thinking_tokens"`
}

// Response is the model's answer exactly as returned.
type Response struct {
	Output       string
	Stop         StopReason
	StopDetail   string // the provider's own reason, such as "MAX_TOKENS"
	Usage        Usage
	ModelVersion string // the model that answered, as the provider names it
}

// Failed reports whether the answer can't be used: anything but StopEnd is a
// failed attempt, since its output may not match the schema.
func (r Response) Failed() error {
	if r.Stop == StopEnd {
		return nil
	}
	return fmt.Errorf("%w: %s (%s)", ErrStopped, r.Stop, r.StopDetail)
}

// Errors a caller can test for with errors.Is.
var (
	// ErrStopped: the model stopped for a reason other than finishing.
	ErrStopped = errors.New("llm: the model didn't finish")
	// ErrCassetteMiss: replay found no cassette for a request.
	ErrCassetteMiss = errors.New("llm: no cassette for this request")
	// ErrRateLimited: the provider answered 429. A run stops on it.
	ErrRateLimited = errors.New("llm: the provider's rate limit was reached")
	// ErrUnavailable: the provider answered 5xx.
	ErrUnavailable = errors.New("llm: the provider is unavailable")
	// ErrDailyLimit: the client-side limiter has used the day's requests.
	ErrDailyLimit = errors.New("llm: the day's request limit is used")
	// ErrUnsupported: the adapter can't serve the request.
	ErrUnsupported = errors.New("llm: the provider doesn't support this request")
)

var (
	namePattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Validate checks a request before it is hashed or sent.
func (r Request) Validate() error {
	for _, f := range []struct{ name, v string }{
		{"job", r.Job}, {"provider", r.Provider}, {"model", r.Model},
		{"prompt ID", r.Prompt.ID}, {"prompt version", r.Prompt.Version},
		{"schema ID", r.Schema.ID}, {"schema version", r.Schema.Version},
	} {
		if !namePattern.MatchString(f.v) {
			return fmt.Errorf("llm: %s %q isn't a valid name", f.name, f.v)
		}
	}
	if len(r.Prompt.Text) == 0 {
		return errors.New("llm: empty prompt")
	}
	if len(r.Schema.JSON) == 0 {
		return errors.New("llm: empty schema")
	}
	if r.MaxOutputTokens <= 0 {
		return errors.New("llm: the token cap must be positive")
	}
	if r.Sample < 0 {
		return errors.New("llm: negative sample index")
	}
	if len(r.Parts) == 0 {
		return errors.New("llm: no content parts")
	}
	for i, p := range r.Parts {
		if err := p.validate(); err != nil {
			return fmt.Errorf("llm: part %d: %w", i, err)
		}
	}
	return nil
}

func (p Part) validate() error {
	switch p.MIMEType {
	case MIMEText:
		if p.Origin != nil {
			return errors.New("a text part has no origin; it is identified by its bytes")
		}
		if len(p.Data) == 0 {
			return errors.New("empty text part")
		}
		return nil
	case MIMEPDF, MIMEPNG, MIMEJPEG:
	default:
		return fmt.Errorf("unsupported MIME type %q", p.MIMEType)
	}
	o := p.Origin
	if o == nil {
		return errors.New("a file part needs an origin")
	}
	if !sha256Pattern.MatchString(o.FileSHA256) {
		return errors.New("the origin's file SHA-256 must be 64 lowercase hex digits")
	}
	if len(p.Data) == 0 {
		return errors.New("empty file part")
	}
	if o.Derivation < 0 {
		return errors.New("negative derivation version")
	}
	if o.Derivation == 0 {
		// The uploaded file itself: its bytes must be the file named.
		if o.Pages != nil {
			return errors.New("pages taken from a file make it derived: set a derivation version")
		}
		if sum := sha256.Sum256(p.Data); hex.EncodeToString(sum[:]) != o.FileSHA256 {
			return errors.New("the part's bytes aren't the file its origin names")
		}
	}
	for i, pg := range o.Pages {
		if pg < 1 || (i > 0 && pg <= o.Pages[i-1]) {
			return errors.New("pages must be ascending, distinct and from 1")
		}
	}
	return nil
}

// checkCapabilities fails a request its provider can't serve.
func checkCapabilities(c Capabilities, r Request) error {
	if !c.JSONSchemaOutput {
		return fmt.Errorf("%w: schema-constrained output", ErrUnsupported)
	}
	for _, p := range r.Parts {
		switch {
		case p.MIMEType == MIMEPDF && !c.PDFInput:
			return fmt.Errorf("%w: PDF input", ErrUnsupported)
		case (p.MIMEType == MIMEPNG || p.MIMEType == MIMEJPEG) && !c.ImageInput:
			return fmt.Errorf("%w: image input", ErrUnsupported)
		}
	}
	if r.Thinking != "" && !slices.Contains(c.ThinkingLevels, r.Thinking) {
		return fmt.Errorf("%w: thinking %q", ErrUnsupported, r.Thinking)
	}
	return nil
}
