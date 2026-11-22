package rules_test

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// explanationGolden pins the JSON shape of adjustments and flags, which M3
// stores in the adjustments table and in audit details. The engine can't
// read files, so the file is compiled in; on a difference the test prints
// what the engine wrote. A change to the file is a change to what M3 stores,
// so it needs a reason, never just a test to pass.
//
//go:embed testdata/explanation.golden.json
var explanationGolden []byte

// explanationClaim is validClaim with an entry of every kind: an adjustment
// citing a clause, one citing none (a package-rate line), inputs of every
// rule's step, a flag about a line, a claim-level flag with a figure and one
// without.
func explanationClaim(t *testing.T) (rules.Facts, rules.Cover) {
	t.Helper()
	f, c := validClaim(t)
	// The room at ₹6,000 a day is over its ₹5,000 cap, so it is capped and
	// the fee on the same day scaled.
	f.Lines[1].UnitPrice, f.Lines[1].Amount = 600_000, 1_200_000
	// The cataract at ₹50,000 is over its ₹40,000 limit.
	f.Lines[3].UnitPrice, f.Lines[3].Amount = 5_000_000, 5_000_000
	f.Lines = append(f.Lines,
		rules.Line{ID: "L06", Category: rules.CategoryProfessionalFees, ServiceDate: date(t, "2025-06-10"),
			Quantity: 1, UnitPrice: 1_000_000, Amount: 1_000_000},
		// A scaled line with no date, in a stay with two rates.
		rules.Line{ID: "L07", Category: rules.CategoryProfessionalFees, Quantity: 1, UnitPrice: 300_000, Amount: 300_000},
		rules.Line{ID: "L08", Category: rules.CategoryOperationTheatre, ServiceDate: date(t, "2025-06-11"),
			Quantity: 1, UnitPrice: 2_000_000, Amount: 2_000_000, PackageRate: true},
	)
	f.BillDiscount = 100_000
	// A claim-level flag with no figure.
	f.DifferentialBilling = rules.AnswerUnknown
	return f, c
}

// explanationJSON writes the adjustments and flags as one JSON document,
// each entry compact on its own line, so the file reads and diffs a line at
// a time.
func explanationJSON(t *testing.T, res rules.Result) []byte {
	t.Helper()
	var b bytes.Buffer
	list := func(key string, n int, entry func(i int) any) {
		b.WriteString("  \"" + key + "\": [")
		for i := range n {
			if i > 0 {
				b.WriteByte(',')
			}
			e, err := json.Marshal(entry(i))
			if err != nil {
				t.Fatal(err)
			}
			b.WriteString("\n    ")
			b.Write(e)
		}
		if n > 0 {
			b.WriteString("\n  ")
		}
		b.WriteByte(']')
	}
	b.WriteString("{\n")
	list("adjustments", len(res.Adjustments), func(i int) any { return res.Adjustments[i] })
	b.WriteString(",\n")
	list("flags", len(res.Flags), func(i int) any { return res.Flags[i] })
	b.WriteString("\n}\n")
	if !json.Valid(b.Bytes()) {
		t.Fatalf("explanationJSON wrote invalid JSON:\n%s", b.Bytes())
	}
	return b.Bytes()
}

func TestExplanationJSON(t *testing.T) {
	pack := goDigitPack(t)
	f, c := explanationClaim(t)
	res, err := rules.Adjudicate(pack, f, c)
	if err != nil {
		t.Fatal(err)
	}
	got := explanationJSON(t, res)
	if !bytes.Equal(got, explanationGolden) {
		t.Errorf("the JSON differs from testdata/explanation.golden.json; the engine wrote:\n%s", got)
	}

	// Whatever the figures, the JSON holds only IDs, reason codes and
	// integer amounts.
	known := map[string]bool{"": true} // an unmodelled rule's clause ID
	for _, l := range f.Lines {
		known[l.ID] = true
	}
	for _, s := range pack.Steps {
		for _, r := range s.Rules {
			known[r.ID], known[r.Clause] = true, true
		}
	}
	for _, code := range rules.FlagCodes() {
		known[string(code)] = true
	}
	for _, a := range res.Adjustments {
		for _, in := range a.Inputs {
			if !inputName.MatchString(in.Name) {
				t.Errorf("%s on %s has input %q, which isn't a code", a.RuleID, a.LineID, in.Name)
			}
			known[in.Name] = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(got))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	checkJSONValue(t, "", doc, known)
}

var inputName = regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)

// The keys an adjustment or a flag may write, and the document's own two.
var jsonKeys = []string{
	"adjustments", "flags",
	"lineId", "ruleId", "clauseId", "inputs", "name", "value", "amountPaise",
	"code", "figurePaise",
}

func checkJSONValue(t *testing.T, path string, v any, known map[string]bool) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if !slices.Contains(jsonKeys, k) {
				t.Errorf("%s has key %q", path, k)
			}
			checkJSONValue(t, path+"/"+k, v[k], known)
		}
	case []any:
		for _, e := range v {
			checkJSONValue(t, path+"[]", e, known)
		}
	case string:
		if !known[v] {
			t.Errorf("%s is %q, which isn't an ID or a code", path, v)
		}
	case json.Number:
		if strings.ContainsAny(v.String(), ".eE") {
			t.Errorf("%s is %s, which isn't an integer", path, v)
		} else if _, err := v.Int64(); err != nil {
			t.Errorf("%s is %s: %v", path, v, err)
		}
	default:
		t.Errorf("%s is %T %v; only objects, lists, strings and integers belong", path, v, v)
	}
}

// A flag writes its figure exactly when its code carries one, even when
// the figure is zero, and its line ID exactly when it is about a line. An
// adjustment with no inputs writes an empty list.
func TestFlagAndAdjustmentJSON(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"claim flag with a zero figure", rules.Flag{Code: rules.FlagCoverExceeded, HasFigure: true},
			`{"code":"cover_exceeded","figurePaise":0}`},
		{"claim flag with no figure", rules.Flag{Code: rules.FlagProposedDenial},
			`{"code":"proposed_denial"}`},
		{"line flag", rules.Flag{Code: rules.FlagUndatedLine, LineID: "L07"},
			`{"code":"undated_line","lineId":"L07"}`},
		{"adjustment with no inputs", rules.Adjustment{LineID: "L01", RuleID: "co-payment", Clause: "E.II.28(c)", Amount: 5},
			`{"lineId":"L01","ruleId":"co-payment","clauseId":"E.II.28(c)","inputs":[],"amountPaise":5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}
