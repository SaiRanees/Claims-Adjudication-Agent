package extraction

import (
	"bytes"
	_ "embed" // the split file
	"fmt"
	"slices"
	"strings"
)

// The halves a scenario can be in. Prompts and thresholds are tuned on the
// dev half; the test half is run once per release candidate, and no model
// call or metric touches it without --release (week 3, session 8).
const (
	HalfDev  = "dev"
	HalfTest = "test"
)

// SplitHeader is the split file's first line.
const SplitHeader = "scenario,claim_type,half"

// SplitEntry is one row of the split file.
type SplitEntry struct {
	Scenario  string
	ClaimType string
	Half      string
}

// String is the entry as the file prints it.
func (e SplitEntry) String() string { return e.Scenario + "," + e.ClaimType + "," + e.Half }

//go:embed split.csv
var splitFile []byte

// SplitFile is the committed split file's bytes.
func SplitFile() []byte { return slices.Clone(splitFile) }

// Split reads the committed split file: each scenario's claim type and
// half, for good. The file is append-only (see `datagen split`).
func Split() ([]SplitEntry, error) { return ParseSplit(splitFile) }

// ParseSplit reads a split file: the header, then one entry a line, each
// with a claim type from ClaimTypes and a half, dev or test.
func ParseSplit(src []byte) ([]SplitEntry, error) {
	lines := strings.Split(string(bytes.TrimSuffix(src, []byte("\n"))), "\n")
	if lines[0] != SplitHeader {
		return nil, fmt.Errorf("split: the header is %q, want %q", lines[0], SplitHeader)
	}
	var out []SplitEntry
	for i, l := range lines[1:] {
		f := strings.Split(l, ",")
		if len(f) != 3 || f[0] == "" || !slices.Contains(ClaimTypes, f[1]) || (f[2] != HalfDev && f[2] != HalfTest) {
			return nil, fmt.Errorf("split: line %d, %q, isn't scenario,claim_type,dev|test", i+2, l)
		}
		out = append(out, SplitEntry{Scenario: f[0], ClaimType: f[1], Half: f[2]})
	}
	return out, nil
}
