package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// HashVersion is bumped by hand if what the request hash covers, or how it is
// encoded, ever changes. Every cassette then misses, deliberately.
const HashVersion = 1

// hashDomain separates request hashes from any other SHA-256 in the system.
const hashDomain = "claims-agent/llm-request\n"

// RequestSummary is everything the request hash covers (ADR 0014), as
// identities, never document bytes. It is stored in each cassette. Its
// encoding is canonical: a struct (fixed field order), slices, and no maps,
// so it encodes the same on every OS.
type RequestSummary struct {
	HashVersion     int           `json:"hash_version"`
	Provider        string        `json:"provider"`
	Model           string        `json:"model"`
	Job             string        `json:"job"`
	Prompt          ItemSummary   `json:"prompt"`
	Schema          ItemSummary   `json:"schema"`
	MaxOutputTokens int           `json:"max_output_tokens"`
	Thinking        string        `json:"thinking"`
	Effort          string        `json:"effort"`
	Parts           []PartSummary `json:"parts"`
	Sample          int           `json:"sample"`
}

// ItemSummary identifies a prompt or a schema by ID, version and SHA-256.
type ItemSummary struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// PartSummary identifies one content part: a text part by the SHA-256 of
// its bytes, a file part by its origin.
type PartSummary struct {
	MIMEType   string  `json:"mime_type"`
	TextSHA256 string  `json:"text_sha256"`
	Origin     *Origin `json:"origin"`
}

// Summarize validates a request and returns what its hash covers.
func Summarize(r Request) (RequestSummary, error) {
	if err := r.Validate(); err != nil {
		return RequestSummary{}, err
	}
	var schema bytes.Buffer
	if err := json.Compact(&schema, r.Schema.JSON); err != nil {
		return RequestSummary{}, fmt.Errorf("llm: schema %s isn't JSON: %w", r.Schema.ID, err)
	}
	s := RequestSummary{
		HashVersion:     HashVersion,
		Provider:        r.Provider,
		Model:           r.Model,
		Job:             r.Job,
		Prompt:          ItemSummary{ID: r.Prompt.ID, Version: r.Prompt.Version, SHA256: sha(r.Prompt.Text)},
		Schema:          ItemSummary{ID: r.Schema.ID, Version: r.Schema.Version, SHA256: sha(schema.Bytes())},
		MaxOutputTokens: r.MaxOutputTokens,
		Thinking:        r.Thinking,
		Effort:          r.Effort,
		Parts:           make([]PartSummary, 0, len(r.Parts)),
		Sample:          r.Sample,
	}
	for _, p := range r.Parts {
		ps := PartSummary{MIMEType: p.MIMEType}
		if p.MIMEType == MIMEText {
			ps.TextSHA256 = sha(p.Data)
		} else {
			o := *p.Origin
			if o.Pages != nil {
				o.Pages = append([]int(nil), o.Pages...)
			}
			ps.Origin = &o
		}
		s.Parts = append(s.Parts, ps)
	}
	return s, nil
}

// Hash returns the summary's request hash: lowercase hex SHA-256.
func (s RequestSummary) Hash() (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("llm: encoding the request summary: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(hashDomain))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RequestHash summarizes a request and hashes it.
func RequestHash(r Request) (string, error) {
	s, err := Summarize(r)
	if err != nil {
		return "", err
	}
	return s.Hash()
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
