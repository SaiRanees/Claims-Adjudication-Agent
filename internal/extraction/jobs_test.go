package extraction

import (
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
)

var jobTypes = map[string]any{
	JobPageClassification: PageClassification{},
	JobLineItems:          LineItems{},
}

// TestSchemaBounds holds every job's schema to llm's bounds (ADR 0016):
// Google publishes none, so these are ours, and every property is required
// (ADR 0015). The live smoke test proves Google accepts each one.
func TestSchemaBounds(t *testing.T) {
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range slices.Sorted(maps.Keys(cfg.Jobs)) {
		s, err := Schema(job, cfg.Jobs[job].SchemaVersion)
		if err != nil {
			t.Fatal(err)
		}
		st, err := llm.CheckSchema(s.JSON)
		if err != nil {
			t.Errorf("%s: %v", job, err)
		}
		if st.Optional != 0 {
			t.Errorf("%s: %d optional properties; every property is required", job, st.Optional)
		}
		t.Logf("%s.%s: %+v", job, s.Version, st)
	}
}

// TestTypesMatchSchemas checks each job's Go type has exactly its schema's
// properties, at every level, so strict decoding can't drop a field.
func TestTypesMatchSchemas(t *testing.T) {
	for job, v := range jobTypes {
		s, err := Schema(job, "v1")
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(s.JSON, &raw); err != nil {
			t.Fatal(err)
		}
		compare(t, job, raw, reflect.TypeOf(v))
	}
}

func compare(t *testing.T, path string, schema map[string]any, typ reflect.Type) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
		typ = typ.Elem()
	}
	if items, ok := schema["items"].(map[string]any); ok {
		compare(t, path+"[]", items, typ)
		return
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return
	}
	if typ.Kind() != reflect.Struct {
		t.Errorf("%s: schema object, Go %s", path, typ)
		return
	}
	fields := map[string]reflect.Type{}
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		fields[name] = f.Type
	}
	if got, want := slices.Sorted(maps.Keys(fields)), slices.Sorted(maps.Keys(props)); !slices.Equal(got, want) {
		t.Errorf("%s: Go fields %v, schema properties %v", path, got, want)
	}
	for name, p := range props {
		if ft, ok := fields[name]; ok {
			compare(t, path+"."+name, p.(map[string]any), ft)
		}
	}
}

func TestNewRequest(t *testing.T) {
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	for job := range jobTypes {
		r, err := NewRequest(cfg, job, []llm.Part{llm.TextPart("page")}, 0)
		if err != nil {
			t.Fatalf("%s: %v", job, err)
		}
		j := cfg.Jobs[job]
		if r.Model != j.Model || r.Thinking != j.Thinking || r.Prompt.Version != j.PromptVersion || r.Schema.ID != job {
			t.Errorf("%s: built %+v", job, r)
		}
	}
	if _, err := NewRequest(cfg, "discharge_summary", []llm.Part{llm.TextPart("x")}, 0); err == nil {
		t.Error("an unknown job: no error")
	}
}
