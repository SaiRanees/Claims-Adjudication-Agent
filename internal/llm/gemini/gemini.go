// Package gemini is the llm.Provider for Google's Gemini Developer API, on the
// free tier (ADR 0016), through google.golang.org/genai.
//
// The API key is handed to the SDK and kept nowhere else: it is never
// logged, printed or put in an error. The SDK sends it in a header, not the
// URL, so a transport error doesn't carry it either.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
)

// Name is the provider's name in config, requests and cassettes.
const Name = "gemini"

// apiVersion is the Gemini API version the adapter calls.
const apiVersion = "v1beta"

// Adapter calls Gemini. It makes one attempt per call: the SDK's retries are
// off, since stacked retries multiply calls against a daily limit.
type Adapter struct {
	client *genai.Client
}

// Options are for tests: BaseURL points the SDK at a fake server.
type Options struct {
	BaseURL    string
	HTTPClient *http.Client
}

// New returns an adapter for the key. An empty key is an error, so a missing
// GEMINI_API_KEY never falls back to whatever the SDK finds in the
// environment.
func New(ctx context.Context, apiKey string, o Options) (*Adapter, error) {
	if apiKey == "" {
		return nil, errors.New("gemini: no API key (GEMINI_API_KEY)")
	}
	one := int32(1)
	cfg := &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: o.HTTPClient,
		HTTPOptions: genai.HTTPOptions{
			APIVersion:   apiVersion,
			BaseURL:      o.BaseURL,
			RetryOptions: &genai.HTTPRetryOptions{Attempts: &one},
		},
	}
	c, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini: creating the client: %w", err)
	}
	return &Adapter{client: c}, nil
}

// Name implements llm.Provider.
func (a *Adapter) Name() string { return Name }

// Capabilities implements llm.Provider. Thinking levels are the API's; which
// a model accepts is in ADR 0016 and config.
func (a *Adapter) Capabilities() llm.Capabilities {
	return llm.Capabilities{
		PDFInput:         true,
		ImageInput:       true,
		JSONSchemaOutput: true,
		ThinkingLevels:   []string{"minimal", "low", "medium", "high", "xhigh"},
	}
}

// Generate implements llm.Provider.
func (a *Adapter) Generate(ctx context.Context, req llm.Request) (llm.Response, error) {
	contents, cfg, err := buildRequest(req)
	if err != nil {
		return llm.Response{}, err
	}
	resp, err := a.client.Models.GenerateContent(ctx, req.Model, contents, cfg)
	if err != nil {
		return llm.Response{}, classify(err)
	}
	return toResponse(resp)
}

// buildRequest maps a request onto the SDK's types: the prompt as the system
// instruction, the parts in order as one user turn, PDFs and images inline,
// JSON output with the job's schema, and the job's thinking level. The
// sample index is never sent.
func buildRequest(req llm.Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	if req.Provider != Name {
		return nil, nil, fmt.Errorf("gemini: request is for provider %s", req.Provider)
	}
	parts := make([]*genai.Part, 0, len(req.Parts))
	for _, p := range req.Parts {
		if p.MIMEType == llm.MIMEText {
			parts = append(parts, genai.NewPartFromText(string(p.Data)))
			continue
		}
		parts = append(parts, genai.NewPartFromBytes(p.Data, p.MIMEType))
	}
	if !json.Valid(req.Schema.JSON) {
		return nil, nil, errors.New("gemini: the schema isn't JSON")
	}
	if req.MaxOutputTokens <= 0 || req.MaxOutputTokens > 1<<20 {
		return nil, nil, fmt.Errorf("gemini: token cap %d out of range", req.MaxOutputTokens)
	}
	cfg := &genai.GenerateContentConfig{
		SystemInstruction:  genai.NewContentFromText(string(req.Prompt.Text), genai.RoleUser),
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: json.RawMessage(req.Schema.JSON),
		MaxOutputTokens:    int32(req.MaxOutputTokens),
		CandidateCount:     1,
	}
	if req.Thinking != "" {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevel(strings.ToUpper(req.Thinking))}
	}
	return []*genai.Content{genai.NewContentFromParts(parts, genai.RoleUser)}, cfg, nil
}

// toResponse takes the first candidate's text, leaving out thought parts.
func toResponse(r *genai.GenerateContentResponse) (llm.Response, error) {
	if r == nil {
		return llm.Response{}, errors.New("gemini: empty response")
	}
	out := llm.Response{ModelVersion: r.ModelVersion}
	if u := r.UsageMetadata; u != nil {
		out.Usage = llm.Usage{
			InputTokens:       int64(u.PromptTokenCount),
			CachedInputTokens: int64(u.CachedContentTokenCount),
			OutputTokens:      int64(u.CandidatesTokenCount),
			ThinkingTokens:    int64(u.ThoughtsTokenCount),
		}
	}
	if pf := r.PromptFeedback; pf != nil && pf.BlockReason != "" {
		out.Stop, out.StopDetail = llm.StopBlocked, string(pf.BlockReason)
		return out, nil
	}
	if len(r.Candidates) == 0 || r.Candidates[0] == nil {
		out.Stop, out.StopDetail = llm.StopOther, "NO_CANDIDATES"
		return out, nil
	}
	c := r.Candidates[0]
	var b strings.Builder
	if c.Content != nil {
		for _, p := range c.Content.Parts {
			if p != nil && !p.Thought {
				b.WriteString(p.Text)
			}
		}
	}
	out.Output = b.String()
	out.Stop, out.StopDetail = stopReason(c.FinishReason), string(c.FinishReason)
	return out, nil
}

// stops maps Gemini's finish reasons onto llm's; any other is StopOther.
var stops = map[genai.FinishReason]llm.StopReason{
	genai.FinishReasonStop:                   llm.StopEnd,
	genai.FinishReasonMaxTokens:              llm.StopMaxTokens,
	genai.FinishReasonSafety:                 llm.StopSafety,
	genai.FinishReasonSPII:                   llm.StopSafety,
	genai.FinishReasonProhibitedContent:      llm.StopSafety,
	genai.FinishReasonBlocklist:              llm.StopSafety,
	genai.FinishReasonImageSafety:            llm.StopSafety,
	genai.FinishReasonImageProhibitedContent: llm.StopSafety,
	genai.FinishReasonRecitation:             llm.StopRecitation,
	genai.FinishReasonImageRecitation:        llm.StopRecitation,
}

func stopReason(f genai.FinishReason) llm.StopReason {
	if s, ok := stops[f]; ok {
		return s
	}
	return llm.StopOther
}

// classify maps the SDK's errors onto llm's: 429 is a rate limit, 5xx is
// unavailable. The API's own message is kept; it never holds the key.
func classify(err error) error {
	var apiErr genai.APIError
	var apiErrPtr *genai.APIError
	switch {
	case errors.As(err, &apiErr):
	case errors.As(err, &apiErrPtr) && apiErrPtr != nil:
		apiErr = *apiErrPtr
	default:
		return fmt.Errorf("gemini: %w", err)
	}
	msg := fmt.Sprintf("gemini: %d %s: %s", apiErr.Code, apiErr.Status, apiErr.Message)
	switch {
	case apiErr.Code == http.StatusTooManyRequests:
		return fmt.Errorf("%w (%s)", llm.ErrRateLimited, msg)
	case apiErr.Code >= 500:
		return fmt.Errorf("%w (%s)", llm.ErrUnavailable, msg)
	}
	return errors.New(msg)
}
