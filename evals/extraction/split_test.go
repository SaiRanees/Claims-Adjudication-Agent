package extraction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// firstHundredSHA256 is the SHA-256 of split.csv's header and its first
// 100 entries, each with its newline. The file is append-only: a scenario
// keeps its half for good, so these lines never change. Never update this
// pin to make the test pass; a later scenario is appended by
// `datagen split` after them.
const firstHundredSHA256 = "90060e66c0016fdfdaecdc8771616f0cc4ea3c9c27af8cf793b5e74dd5016476"

func TestSplitFirstHundredPinned(t *testing.T) {
	lines := bytes.SplitAfter(SplitFile(), []byte("\n"))
	if len(lines) < 101 {
		t.Fatalf("split.csv has %d lines, want the header and at least 100 entries", len(lines))
	}
	sum := sha256.Sum256(bytes.Join(lines[:101], nil))
	if got := hex.EncodeToString(sum[:]); got != firstHundredSHA256 {
		t.Errorf("the header and first 100 entries hash to %s, pinned %s: split.csv is append-only", got, firstHundredSHA256)
	}
}

// TestSplitCoversEveryScenario checks that every scenario appears exactly
// once, that the split lists no other, that a seeded scenario's type is
// the one its ID names, and that no type's halves differ by more than one.
func TestSplitCoversEveryScenario(t *testing.T) {
	entries, err := Split()
	if err != nil {
		t.Fatal(err)
	}
	specs, err := Specs()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range specs {
		ids[s.ID] = true
	}
	seen := map[string]int{}
	halves := map[string]int{} // dev counts +1, test -1
	for _, e := range entries {
		seen[e.Scenario]++
		if !ids[e.Scenario] {
			t.Errorf("%s is in the split but has no spec", e.Scenario)
		}
		if strings.HasPrefix(e.Scenario, "seeded-") && !strings.HasPrefix(e.Scenario, "seeded-"+e.ClaimType+"-") {
			t.Errorf("%s is split as %s", e.Scenario, e.ClaimType)
		}
		if e.Half == HalfDev {
			halves[e.ClaimType]++
		} else {
			halves[e.ClaimType]--
		}
	}
	for id := range ids {
		if seen[id] != 1 {
			t.Errorf("%s appears %d times in the split", id, seen[id])
		}
	}
	for _, ct := range ClaimTypes {
		if d := halves[ct]; d < -1 || d > 1 {
			t.Errorf("%s: the dev half has %+d more scenarios than the test half", ct, d)
		}
	}
}

func TestParseSplitRejects(t *testing.T) {
	tests := []struct{ name, src string }{
		{"no header", "golden-01,medical,dev\n"},
		{"an unknown claim type", SplitHeader + "\ngolden-01,maternity,dev\n"},
		{"an unknown half", SplitHeader + "\ngolden-01,medical,train\n"},
		{"a missing field", SplitHeader + "\ngolden-01,medical\n"},
		{"a blank line", SplitHeader + "\n\ngolden-01,medical,dev\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseSplit([]byte(tt.src)); err == nil {
				t.Error("parsed")
			}
		})
	}
}
