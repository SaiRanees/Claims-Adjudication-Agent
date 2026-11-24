package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// A case with one line, paying 950 of 1,000 after a 50 co-payment.
func compareCase() Case {
	return Case{
		ID:    "x",
		Facts: rules.Facts{Lines: []rules.Line{{ID: "L01", Amount: 1000}}},
		Want: Expected{
			Lines:        []WantLine{{ID: "L01", Payable: 950}},
			Adjustments:  []WantAdjustment{{Line: "L01", Rule: "co-payment", Amount: 50}},
			ClaimPayable: 950,
		},
	}
}

func TestCheckedDiffs(t *testing.T) {
	c := compareCase()
	exact := ExpectedRows(c)
	with := func(edit func([]Row) []Row) []Row {
		return edit(append([]Row(nil), exact...))
	}
	tests := []struct {
		name    string
		checked []Row
		wantKey string // the first diff's key; empty for no diffs
	}{
		{name: "unchecked", checked: nil},
		{name: "every row matches", checked: exact},
		{name: "a figure differs", checked: with(func(r []Row) []Row { r[0].Paise++; return r }),
			wantKey: "adjustment|L01|co-payment"},
		{name: "a row missing", checked: with(func(r []Row) []Row { return r[1:] }),
			wantKey: "adjustment|L01|co-payment"},
		{name: "an extra row", checked: with(func(r []Row) []Row {
			return append(r, Row{Kind: KindFlag, Code: string(rules.FlagProposedDenial)})
		}), wantKey: "flag||proposed_denial"},
		{name: "a row repeated", checked: with(func(r []Row) []Row { return append(r[:1], r...) }),
			wantKey: "rows"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diffs := CheckedDiffs(c, tt.checked)
			if tt.wantKey == "" {
				if len(diffs) > 0 {
					t.Errorf("diffs = %v, want none", diffs)
				}
				return
			}
			if len(diffs) == 0 || diffs[0].Key != tt.wantKey {
				t.Errorf("diffs = %v, want the first at %q", diffs, tt.wantKey)
			}
		})
	}
}

func TestVerdict(t *testing.T) {
	differs := []Diff{{Key: "claim_payable||", Want: "1", Got: "2", Compared: true}}
	tests := []struct {
		name string
		o    Outcome
		want Verdict
	}{
		{name: "checked and matching", o: Outcome{Report: Report{Status: StatusPass}, Checked: true}, want: VerdictPass},
		{name: "unchecked, engine matches", o: Outcome{Report: Report{Status: StatusPass}}, want: VerdictUnchecked},
		{name: "unchecked, engine differs", o: Outcome{Report: Report{Status: StatusFail, Diffs: differs}}, want: VerdictFail},
		{name: "checked, engine differs", o: Outcome{Report: Report{Status: StatusFail, Diffs: differs}, Checked: true}, want: VerdictFail},
		{name: "checked, engine error", o: Outcome{Report: Report{Status: StatusFail, Err: errors.New("x")}, Checked: true}, want: VerdictFail},
		{name: "checked, pending", o: Outcome{Report: Report{Status: StatusPending}, Checked: true}, want: VerdictFail},
		{name: "checked rows differ from the case file", o: Outcome{Report: Report{Status: StatusPass}, Checked: true, CheckedDiffs: differs},
			want: VerdictFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.o.Verdict(); got != tt.want {
				t.Errorf("Verdict() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDiffDescribe(t *testing.T) {
	d := Diff{Key: "claim_payable||", Want: "claim_payable: 1 paise", Got: "claim_payable: 2 paise"}
	got := d.Describe("case", "engine")
	if !strings.HasPrefix(got, "claim_payable||: case claim_payable: 1 paise, engine claim_payable: 2 paise") ||
		!strings.HasSuffix(got, "(not compared yet)") {
		t.Errorf("Describe = %q", got)
	}
}
