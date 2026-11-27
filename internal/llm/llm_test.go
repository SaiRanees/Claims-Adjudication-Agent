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

const testSchema = `{
  "type": "object",
  "properties": {
    "kind": {"type": "string", "enum": ["bill", "receipt"]},
    "number": {"type": ["string", "null"]},
    "lines": {
      "type": "array",
      "maxItems": 3,
      "items": {
        "type": "object",
        "properties": {
          "page": {"type": "integer"},
          "amount": {"type": ["string", "null"]}
        },
        "required": ["page", "amount"],
        "additionalProperties": false
      }
    }
  },
  "required": ["kind", "number", "lines"],
  "additionalProperties": false
}`

type testOutput struct {
	Kind   string     `json:"kind"`
	Number *string    `json:"number"`
	Lines  []testLine `json:"lines"`
}

type testLine struct {
	Page   int     `json:"page"`
	Amount *string `json:"amount"`
}

// pinnedRequest is fixed forever: its hash is pinned below and checked on
// CI's Linux and Windows jobs, so the canonical encoding can't drift by OS.
func pinnedRequest() Request {
	pdf := []byte("%PDF-1.7 synthetic test bytes")
	return Request{
		Job: "line_items", Provider: "gemini", Model: "gemini-4-hydrogen",
		Prompt:          Prompt{ID: "line_items", Version: "v1", Text: []byte("Copy every line as printed.\n")},
		Schema:          Schema{ID: "line_items", Version: "v1", JSON: []byte(testSchema)},
		MaxOutputTokens: 16384, Thinking: "low",
		Parts: []Part{
			TextPart("Pages 1 to 2 of the bill."),
			{MIMEType: MIMEPDF, Data: pdf, Origin: &Origin{
				FileSHA256: "0625b8c74fda3dd73cffcf04ca23d8bd4d14b10efced4a3967e334aafc091107",
				Pages:      []int{1, 2}, Derivation: 1,
			}},
		},
	}
}

// The pinned hash. Change it only with HashVersion, never to make CI pass:
// a difference between the Linux and Windows jobs is a bug.
const pinnedHash = "74076db4ee7d33ca219c7afd19d5682c10d383aef1f2ffd7aea4825129d88f20"

func TestPinnedRequestHash(t *testing.T) {
	got, err := RequestHash(pinnedRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got != pinnedHash {
		t.Errorf("request hash %s, pinned %s", got, pinnedHash)
	}
}

func TestHashCoversEverythingButFileBytes(t *testing.T) {
	base, err := RequestHash(pinnedRequest())
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*Request){
		"provider": func(r *Request) { r.Provider = "claude" },
		"model":    func(r *Request) { r.Model = "gemini-3.8-flash" },
		"job":      func(r *Request) { r.Job = "page_classification" },
		"prompt":   func(r *Request) { r.Prompt.Text = []byte("Copy every line.\n") },
		"prompt v": func(r *Request) { r.Prompt.Version = "v2" },
		"schema":   func(r *Request) { r.Schema.JSON = []byte(strings.Replace(testSchema, "receipt", "invoice", 1)) },
		"cap":      func(r *Request) { r.MaxOutputTokens = 8192 },
		"thinking": func(r *Request) { r.Thinking = "high" },
		"effort":   func(r *Request) { r.Effort = "low" },
		"sample":   func(r *Request) { r.Sample = 1 },
		"text":     func(r *Request) { r.Parts[0] = TextPart("Pages 1 to 3 of the bill.") },
		"pages":    func(r *Request) { r.Parts[1].Origin.Pages = []int{1, 3} },
		"deriv":    func(r *Request) { r.Parts[1].Origin.Derivation = 2 },
		"order":    func(r *Request) { r.Parts[0], r.Parts[1] = r.Parts[1], r.Parts[0] },
	}
	for name, change := range changes {
		r := pinnedRequest()
		change(&r)
		h, err := RequestHash(r)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h == base {
			t.Errorf("%s: changing it left the hash unchanged", name)
		}
	}
	// A derived file's bytes aren't hashed: pdfcpu's differ on every run.
	r := pinnedRequest()
	r.Parts[1].Data = []byte("%PDF-1.7 other bytes, same pages")
	if h, _ := RequestHash(r); h != base {
		t.Error("a derived file's bytes changed the hash")
	}
	// Schema whitespace isn't part of the identity.
	r = pinnedRequest()
	r.Schema.JSON = []byte(strings.Join(strings.Fields(testSchema), ""))
	if h, _ := RequestHash(r); h != base {
		t.Error("reformatting the schema changed the hash")
	}
}

func TestValidate(t *testing.T) {
	pdf := []byte("%PDF-1.7 x")
	tests := map[string]func(*Request){
		"bad job":             func(r *Request) { r.Job = "Line Items" },
		"no prompt":           func(r *Request) { r.Prompt.Text = nil },
		"no parts":            func(r *Request) { r.Parts = nil },
		"zero cap":            func(r *Request) { r.MaxOutputTokens = 0 },
		"negative sample":     func(r *Request) { r.Sample = -1 },
		"text with origin":    func(r *Request) { r.Parts[0].Origin = &Origin{} },
		"file without origin": func(r *Request) { r.Parts[1].Origin = nil },
		"bad sha":             func(r *Request) { r.Parts[1].Origin.FileSHA256 = "ABC" },
		"pages unsorted":      func(r *Request) { r.Parts[1].Origin.Pages = []int{2, 1} },
		"pages repeated":      func(r *Request) { r.Parts[1].Origin.Pages = []int{1, 1} },
		"page zero":           func(r *Request) { r.Parts[1].Origin.Pages = []int{0} },
		"pages underived":     func(r *Request) { r.Parts[1].Origin.Derivation = 0 },
		"bytes not the file":  func(r *Request) { r.Parts[1] = FilePart(MIMEPDF, pdf); r.Parts[1].Data = []byte("other") },
		"bad mime":            func(r *Request) { r.Parts[1].MIMEType = "application/zip" },
	}
	for name, change := range tests {
		r := pinnedRequest()
		change(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	r := pinnedRequest()
	r.Parts[1] = FilePart(MIMEPDF, pdf)
	if err := r.Validate(); err != nil {
		t.Errorf("an uploaded file part: %v", err)
	}
}

func TestCheckSchema(t *testing.T) {
	st, err := CheckSchema([]byte(testSchema))
	if err != nil {
		t.Fatal(err)
	}
	want := SchemaStats{Properties: 5, Optional: 0, Nullable: 2, EnumValues: 2, Depth: 4}
	if st != want {
		t.Errorf("stats %+v, want %+v", st, want)
	}
	bad := map[string]string{
		"optional":       strings.Replace(testSchema, `"required": ["kind", "number", "lines"]`, `"required": ["kind", "lines"]`, 1),
		"anyOf":          `{"anyOf": [{"type": "string"}]}`,
		"number type":    `{"type": "object", "properties": {"a": {"type": "number"}}, "required": ["a"], "additionalProperties": false}`,
		"open object":    `{"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a"]}`,
		"no type":        `{"type": "object", "properties": {"a": {}}, "required": ["a"], "additionalProperties": false}`,
		"null first":     `{"type": "object", "properties": {"a": {"type": ["null", "string"]}}, "required": ["a"], "additionalProperties": false}`,
		"unknown key":    `{"type": "string", "pattern": "x"}`,
		"enum on int":    `{"type": "integer", "enum": ["1"]}`,
		"required ghost": `{"type": "object", "properties": {"a": {"type": "string"}}, "required": ["a", "b"], "additionalProperties": false}`,
	}
	for name, s := range bad {
		if _, err := CheckSchema([]byte(s)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// Too many nullable properties.
	var props, req []string
	for i := range MaxNullable + 1 {
		name := "f" + string(rune('a'+i))
		props = append(props, `"`+name+`": {"type": ["string", "null"]}`)
		req = append(req, `"`+name+`"`)
	}
	wide := `{"type": "object", "properties": {` + strings.Join(props, ",") + `}, "required": [` + strings.Join(req, ",") + `], "additionalProperties": false}`
	if _, err := CheckSchema([]byte(wide)); err == nil {
		t.Error("too many nullable properties: no error")
	}
}

func TestDecode(t *testing.T) {
	s := Schema{ID: "t", Version: "v1", JSON: []byte(testSchema)}
	var out testOutput
	if err := Decode(s, `{"kind": "BILL", "number": null, "lines": [{"page": 1, "amount": "1,23,456.00"}]}`, &out); err != nil {
		t.Fatal(err)
	}
	if out.Kind != "bill" || out.Number != nil || len(out.Lines) != 1 || *out.Lines[0].Amount != "1,23,456.00" {
		t.Errorf("decoded %+v", out)
	}
	bad := map[string]string{
		"unknown field":   `{"kind": "bill", "number": null, "lines": [], "extra": 1}`,
		"missing field":   `{"kind": "bill", "lines": []}`,
		"missing nested":  `{"kind": "bill", "number": null, "lines": [{"page": 1}]}`,
		"bad enum":        `{"kind": "invoice", "number": null, "lines": []}`,
		"null required":   `{"kind": null, "number": null, "lines": []}`,
		"float page":      `{"kind": "bill", "number": null, "lines": [{"page": 1.5, "amount": null}]}`,
		"number amount":   `{"kind": "bill", "number": null, "lines": [{"page": 1, "amount": 100}]}`,
		"too many items":  `{"kind": "bill", "number": null, "lines": [{"page":1,"amount":null},{"page":1,"amount":null},{"page":1,"amount":null},{"page":1,"amount":null}]}`,
		"trailing data":   `{"kind": "bill", "number": null, "lines": []} {}`,
		"not json":        `kind: bill`,
		"array not obj":   `[]`,
		"string for null": `{"kind": "bill", "number": 7, "lines": []}`,
	}
	for name, o := range bad {
		var v testOutput
		if err := Decode(s, o, &v); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	// A Go type missing a schema field fails.
	var narrow struct {
		Kind string `json:"kind"`
	}
	if err := Decode(s, `{"kind": "bill", "number": null, "lines": []}`, &narrow); err == nil {
		t.Error("a Go type narrower than the schema: no error")
	}
}

func TestConfig(t *testing.T) {
	c, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"page_classification", "line_items"} {
		j, ok := c.Jobs[job]
		if !ok {
			t.Fatalf("no job %s", job)
		}
		m := c.Models[j.Model]
		if m.Tier != "free" {
			t.Errorf("%s runs on %s, tier %s: ADR 0016 keeps development on the free tier", job, j.Model, m.Tier)
		}
		cost, err := m.Prices.Cost(Usage{InputTokens: 1e6, OutputTokens: 1e6, ThinkingTokens: 1e6}, false)
		if err != nil || cost != 0 {
			t.Errorf("%s: a free-tier call cost %d, %v", job, cost, err)
		}
	}
	bad := map[string]string{
		"unknown field":    strings.Replace(string(modelsYAML), "tier: free", "tier: free\n    region: us", 1),
		"price missing":    strings.Replace(string(modelsYAML), "      cache_read: 0\n", "", 1),
		"free not free":    strings.Replace(string(modelsYAML), "      input: 0\n", "      input: 100000\n", 1),
		"no daily limit":   strings.Replace(string(modelsYAML), "requests_per_day: 20", "requests_per_day: 0", 1),
		"unknown model":    strings.Replace(string(modelsYAML), "model: gemini-4-hydrogen", "model: gemini-9", 1),
		"provider differs": strings.Replace(string(modelsYAML), "  line_items:\n    provider: gemini", "  line_items:\n    provider: claude", 1),
	}
	for name, src := range bad {
		if src == string(modelsYAML) {
			t.Fatalf("%s: the edit didn't apply", name)
		}
		if _, err := ParseConfig([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func i64(v int64) *int64 { return &v }

func TestCost(t *testing.T) {
	// Every figure written out, as a paid model would have them.
	p := Prices{
		Input: i64(2_000_000), Output: i64(10_000_000), CacheWrite: i64(2_500_000), CacheRead: i64(100_000),
		BatchInput: i64(1_000_000), BatchOutput: i64(5_000_000),
		LongPrompt: &LongPrompt{AboveTokens: 200_000, Input: i64(4_000_000), Output: i64(15_000_000), CacheRead: i64(200_000)},
	}
	tests := []struct {
		name  string
		u     Usage
		batch bool
		want  int64
	}{
		{"plain", Usage{InputTokens: 1000, OutputTokens: 500}, false, 2000 + 5000},
		{"thinking billed as output", Usage{InputTokens: 1000, OutputTokens: 100, ThinkingTokens: 400}, false, 2000 + 5000},
		{"cached", Usage{InputTokens: 1000, CachedInputTokens: 600}, false, 800 + 60},
		{"rounds half up", Usage{InputTokens: 1}, false, 2},
		{"batch", Usage{InputTokens: 1000, OutputTokens: 500}, true, 1000 + 2500},
		{"long prompt", Usage{InputTokens: 300_000, OutputTokens: 1000}, false, 1_200_000 + 15_000},
	}
	for _, tt := range tests {
		got, err := p.Cost(tt.u, tt.batch)
		if err != nil || got != tt.want {
			t.Errorf("%s: %d, %v; want %d", tt.name, got, err, tt.want)
		}
	}
	if _, err := p.Cost(Usage{InputTokens: 1, CachedInputTokens: 2}, false); err == nil {
		t.Error("more cached than input: no error")
	}
	if _, err := p.Cost(Usage{OutputTokens: 1 << 62}, false); err == nil {
		t.Error("overflow: no error")
	}
}

// fakeProvider answers every request with one output, counting calls.
type fakeProvider struct {
	calls int
	resp  Response
	err   error
}

func (f *fakeProvider) Name() string { return "gemini" }
func (f *fakeProvider) Capabilities() Capabilities {
	return Capabilities{PDFInput: true, JSONSchemaOutput: true, ThinkingLevels: []string{"low"}}
}
func (f *fakeProvider) Generate(context.Context, Request) (Response, error) {
	f.calls++
	return f.resp, f.err
}

func testConfig(t *testing.T) Config {
	t.Helper()
	c, err := DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientModes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewCassetteStore(dir)
	cfg := testConfig(t)
	lim, err := NewLimiter(cfg.Limits(), "")
	if err != nil {
		t.Fatal(err)
	}
	answer := Response{Output: `{"kind":"bill","number":null,"lines":[]}`, Stop: StopEnd, StopDetail: "STOP",
		Usage: Usage{InputTokens: 900, OutputTokens: 20, ThinkingTokens: 12}, ModelVersion: "gemini-4-hydrogen-001"}
	fake := &fakeProvider{resp: answer}
	now := func() time.Time { return time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC) }
	req := pinnedRequest()

	// Replay with no cassette: a miss naming the job and the hash.
	replay, err := NewClient(Options{Mode: ModeReplay, Cassettes: store, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replay.Call(ctx, req); !errors.Is(err, ErrCassetteMiss) || !strings.Contains(err.Error(), "line_items") {
		t.Fatalf("replay before record: %v", err)
	}

	// Record calls once and writes the cassette; a second record replays.
	rec, err := NewClient(Options{Mode: ModeRecord, Provider: fake, Cassettes: store, Limiter: lim, Config: cfg, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		res, err := rec.Call(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if res.Output != answer.Output || res.Replayed != (i == 1) {
			t.Errorf("record call %d: %+v", i, res)
		}
	}
	if fake.calls != 1 {
		t.Errorf("record made %d calls for one request, want 1", fake.calls)
	}

	// The cassette is LF-only JSON at <job>/<hash>.json.
	hash, _ := RequestHash(req)
	b, err := os.ReadFile(filepath.Join(dir, "line_items", hash+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "\r") || !strings.Contains(string(b), `"recorded_at": "2026-11-27T10:00:00Z"`) {
		t.Errorf("cassette:\n%s", b)
	}

	// Replay now answers from it without a provider.
	res, err := replay.Call(ctx, req)
	if err != nil || !res.Replayed || res.Usage != answer.Usage || res.ModelVersion != answer.ModelVersion || res.Hash != hash {
		t.Errorf("replay: %+v, %v", res, err)
	}

	// Rerecord replaces deliberately.
	rerec, err := NewClient(Options{Mode: ModeRecord, Provider: fake, Cassettes: store, Limiter: lim, Config: cfg, Rerecord: []string{"line_items"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rerec.Call(ctx, req); err != nil || fake.calls != 2 {
		t.Errorf("rerecord: calls %d, %v", fake.calls, err)
	}

	// Live always calls and writes nothing.
	live, err := NewClient(Options{Mode: ModeLive, Provider: fake, Limiter: lim, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	other := pinnedRequest()
	other.Sample = 1
	if _, err := live.Call(ctx, other); err != nil || fake.calls != 3 {
		t.Errorf("live: calls %d, %v", fake.calls, err)
	}
	otherHash, _ := RequestHash(other)
	if _, err := os.Stat(filepath.Join(dir, "line_items", otherHash+".json")); err == nil {
		t.Error("live wrote a cassette")
	}

	// A failed stop is recorded like any answer, and replays as failed.
	fake.resp = Response{Stop: StopMaxTokens, StopDetail: "MAX_TOKENS"}
	third := pinnedRequest()
	third.Sample = 2
	if res, err := rec.Call(ctx, third); err != nil || !errors.Is(res.Failed(), ErrStopped) {
		t.Errorf("max tokens: %+v, %v", res, err)
	}
	if res, err := replay.Call(ctx, third); err != nil || res.Stop != StopMaxTokens {
		t.Errorf("replayed max tokens: %+v, %v", res, err)
	}

	// A provider error writes nothing and passes through.
	fake.err = ErrRateLimited
	fourth := pinnedRequest()
	fourth.Sample = 3
	if _, err := rec.Call(ctx, fourth); !errors.Is(err, ErrRateLimited) {
		t.Errorf("rate limited: %v", err)
	}
	if _, err := replay.Call(ctx, fourth); !errors.Is(err, ErrCassetteMiss) {
		t.Errorf("a failed call left a cassette: %v", err)
	}
}

func TestCassetteTampering(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := NewCassetteStore(dir)
	cfg := testConfig(t)
	lim, _ := NewLimiter(cfg.Limits(), "")
	fake := &fakeProvider{resp: Response{Output: "{}", Stop: StopEnd}}
	rec, _ := NewClient(Options{Mode: ModeRecord, Provider: fake, Cassettes: store, Limiter: lim, Config: cfg})
	req := pinnedRequest()
	if _, err := rec.Call(ctx, req); err != nil {
		t.Fatal(err)
	}
	hash, _ := RequestHash(req)
	path := filepath.Join(dir, "line_items", hash+".json")
	orig, _ := os.ReadFile(path)
	replay, _ := NewClient(Options{Mode: ModeReplay, Cassettes: store, Config: cfg})
	for name, edit := range map[string]func(string) string{
		"request edited": func(s string) string { return strings.Replace(s, `"thinking": "low"`, `"thinking": "high"`, 1) },
		"unknown field":  func(s string) string { return strings.Replace(s, `"format": 1,`, `"format": 1, "note": "x",`, 1) },
		"format":         func(s string) string { return strings.Replace(s, `"format": 1,`, `"format": 2,`, 1) },
	} {
		edited := edit(string(orig))
		if edited == string(orig) {
			t.Fatalf("%s: the edit didn't apply", name)
		}
		if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := replay.Call(ctx, req); err == nil || errors.Is(err, ErrCassetteMiss) {
			t.Errorf("%s: %v, want an error that isn't a miss", name, err)
		}
	}
}

func TestClientRefuses(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	lim, _ := NewLimiter(cfg.Limits(), "")
	fake := &fakeProvider{resp: Response{Stop: StopEnd}}
	live, _ := NewClient(Options{Mode: ModeLive, Provider: fake, Limiter: lim, Config: cfg})
	r := pinnedRequest()
	r.Thinking = "minimal"
	if _, err := live.Call(ctx, r); !errors.Is(err, ErrUnsupported) {
		t.Errorf("unsupported thinking: %v", err)
	}
	r = pinnedRequest()
	r.Provider = "claude"
	if _, err := live.Call(ctx, r); err == nil {
		t.Error("another provider's request: no error")
	}
	if fake.calls != 0 {
		t.Errorf("%d calls for requests that should fail first", fake.calls)
	}
	for _, o := range []Options{
		{Mode: "batch"},
		{Mode: ModeReplay},
		{Mode: ModeLive, Limiter: lim},
		{Mode: ModeLive, Provider: fake, Limiter: lim, Rerecord: []string{"line_items"}},
	} {
		if _, err := NewClient(o); err == nil {
			t.Errorf("options %+v: no error", o)
		}
	}
	if RequireLive(ModeLive) != nil || RequireLive(ModeRecord) == nil || RequireLive(ModeReplay) == nil {
		t.Error("RequireLive must accept live only")
	}
}

func TestBuildRequest(t *testing.T) {
	cfg := testConfig(t)
	p := Prompt{ID: "line_items", Version: "v1", Text: []byte("x")}
	s := Schema{ID: "line_items", Version: "v1", JSON: []byte(testSchema)}
	r, err := cfg.BuildRequest("line_items", p, s, []Part{TextPart("hello")}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "gemini-4-hydrogen" || r.Thinking != "low" || r.MaxOutputTokens != 16384 || r.Provider != "gemini" {
		t.Errorf("built %+v", r)
	}
	p.Version = "v2"
	if _, err := cfg.BuildRequest("line_items", p, s, []Part{TextPart("hello")}, 0); err == nil {
		t.Error("a prompt version config doesn't name: no error")
	}
}
