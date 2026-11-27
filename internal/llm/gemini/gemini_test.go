package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
)

const fakeKey = "test-key-never-printed-0123456789"

const schema = `{"type":"object","properties":{"kind":{"type":"string","enum":["bill"]}},"required":["kind"],"additionalProperties":false}`

func request() llm.Request {
	return llm.Request{
		Job: "line_items", Provider: Name, Model: "gemini-4-hydrogen",
		Prompt:          llm.Prompt{ID: "line_items", Version: "v1", Text: []byte("Copy every line.")},
		Schema:          llm.Schema{ID: "line_items", Version: "v1", JSON: []byte(schema)},
		MaxOutputTokens: 1024, Thinking: "low", Sample: 7,
		Parts: []llm.Part{llm.TextPart("Here is the bill."), llm.FilePart(llm.MIMEPDF, []byte("%PDF-1.7 synthetic"))},
	}
}

// server answers with status and body, recording each request it gets.
func server(t *testing.T, status int, body string) (*httptest.Server, *[]map[string]any, *atomic.Int32) {
	t.Helper()
	var got []map[string]any
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("x-goog-api-key") != fakeKey {
			t.Errorf("the key wasn't sent in the x-goog-api-key header")
		}
		if strings.Contains(r.URL.String(), fakeKey) {
			t.Errorf("the key is in the URL")
		}
		if !strings.HasSuffix(r.URL.Path, "/v1beta/models/gemini-4-hydrogen:generateContent") {
			t.Errorf("path %s", r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Errorf("request body: %v", err)
		}
		got = append(got, m)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got, &calls
}

func adapter(t *testing.T, url string) *Adapter {
	t.Helper()
	a, err := New(context.Background(), fakeKey, Options{BaseURL: url + "/", HTTPClient: http.DefaultClient})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestGenerate(t *testing.T) {
	body := `{
	  "candidates": [{"content": {"role": "model", "parts": [
	    {"text": "thinking about it", "thought": true},
	    {"text": "{\"kind\":"}, {"text": "\"bill\"}"}
	  ]}, "finishReason": "STOP"}],
	  "usageMetadata": {"promptTokenCount": 1200, "candidatesTokenCount": 9, "thoughtsTokenCount": 30, "cachedContentTokenCount": 0},
	  "modelVersion": "gemini-4-hydrogen-001"
	}`
	srv, got, _ := server(t, 200, body)
	resp, err := adapter(t, srv.URL).Generate(context.Background(), request())
	if err != nil {
		t.Fatal(err)
	}
	want := llm.Response{Output: `{"kind":"bill"}`, Stop: llm.StopEnd, StopDetail: "STOP", ModelVersion: "gemini-4-hydrogen-001",
		Usage: llm.Usage{InputTokens: 1200, OutputTokens: 9, ThinkingTokens: 30}}
	if resp != want {
		t.Errorf("response %+v, want %+v", resp, want)
	}
	if len(*got) != 1 {
		t.Fatalf("%d requests", len(*got))
	}
	sent, _ := json.Marshal((*got)[0])
	s := string(sent)
	for _, w := range []string{
		`"responseMimeType":"application/json"`,
		`"responseJsonSchema":{"additionalProperties":false`,
		`"thinkingLevel":"LOW"`,
		`"maxOutputTokens":1024`,
		`"mimeType":"application/pdf"`,
		`"text":"Here is the bill."`,
		`"systemInstruction"`,
		`"text":"Copy every line."`,
	} {
		if !strings.Contains(s, w) {
			t.Errorf("request lacks %s:\n%s", w, s)
		}
	}
	if strings.Contains(s, "sample") || strings.Contains(s, ":7") {
		t.Errorf("the sample index was sent:\n%s", s)
	}
}

func TestGenerateStops(t *testing.T) {
	tests := []struct {
		name string
		body string
		stop llm.StopReason
	}{
		{"max tokens", `{"candidates":[{"content":{"parts":[{"text":"{\"ki"}]},"finishReason":"MAX_TOKENS"}]}`, llm.StopMaxTokens},
		{"safety", `{"candidates":[{"finishReason":"SAFETY"}]}`, llm.StopSafety},
		{"recitation", `{"candidates":[{"finishReason":"RECITATION"}]}`, llm.StopRecitation},
		{"blocked prompt", `{"promptFeedback":{"blockReason":"SAFETY"}}`, llm.StopBlocked},
		{"no candidates", `{}`, llm.StopOther},
		{"other", `{"candidates":[{"finishReason":"MALFORMED_FUNCTION_CALL"}]}`, llm.StopOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _, _ := server(t, 200, tt.body)
			resp, err := adapter(t, srv.URL).Generate(context.Background(), request())
			if err != nil {
				t.Fatal(err)
			}
			if resp.Stop != tt.stop || resp.Failed() == nil {
				t.Errorf("stop %s (%v), want %s and a failed attempt", resp.Stop, resp.Failed(), tt.stop)
			}
		})
	}
}

func TestGenerateErrors(t *testing.T) {
	tests := []struct {
		status int
		want   error // nil: neither a rate limit nor unavailable
	}{
		{429, llm.ErrRateLimited},
		{503, llm.ErrUnavailable},
		{500, llm.ErrUnavailable},
		{400, nil},
	}
	for _, tt := range tests {
		body := fmt.Sprintf(`{"error":{"code":%d,"message":"something went wrong","status":"X"}}`, tt.status)
		srv, _, calls := server(t, tt.status, body)
		_, err := adapter(t, srv.URL).Generate(context.Background(), request())
		if err == nil {
			t.Fatalf("%d: no error", tt.status)
		}
		if tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("%d: %v, want %v", tt.status, err, tt.want)
		}
		if tt.want == nil && (errors.Is(err, llm.ErrRateLimited) || errors.Is(err, llm.ErrUnavailable)) {
			t.Errorf("%d: %v", tt.status, err)
		}
		if strings.Contains(err.Error(), fakeKey) {
			t.Errorf("%d: the key is in the error", tt.status)
		}
		if n := calls.Load(); n != 1 {
			t.Errorf("%d: %d attempts; the SDK's retries must be off", tt.status, n)
		}
	}
}

func TestNewNeedsKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "from-the-environment")
	t.Setenv("GOOGLE_API_KEY", "from-the-environment")
	if _, err := New(context.Background(), "", Options{}); err == nil {
		t.Error("an empty key fell back to the environment")
	}
}

func TestBuildRequestRefuses(t *testing.T) {
	r := request()
	r.Provider = "claude"
	if _, _, err := buildRequest(r); err == nil {
		t.Error("another provider's request: no error")
	}
	r = request()
	r.MaxOutputTokens = 1 << 21
	if _, _, err := buildRequest(r); err == nil {
		t.Error("an absurd token cap: no error")
	}
}
