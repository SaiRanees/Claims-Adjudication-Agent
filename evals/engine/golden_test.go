package engine_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/engine"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

func loadCases(t *testing.T) []engine.Case {
	t.Helper()
	cases, err := engine.Cases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no golden cases")
	}
	return cases
}

func registry(t *testing.T) *rules.Registry {
	t.Helper()
	reg, err := rulepacks.Registry()
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// checked reads golden-cases.csv. Until the first export, there is none and
// every case is unchecked.
func checked(t *testing.T) map[string][]engine.Row {
	t.Helper()
	data, err := os.ReadFile(engine.CSVPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	rows, err := engine.ParseCSV(data)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// TestGoldenCasesConsistent checks each case file against itself, so a slip
// in the hand arithmetic shows up before anyone checks the figures: every
// line has one payable, billed less payable equals the line's adjustments,
// the lines add up to the claim, and every rule and flag exists.
func TestGoldenCasesConsistent(t *testing.T) {
	reg := registry(t)
	for _, c := range loadCases(t) {
		t.Run(c.ID, func(t *testing.T) {
			pack, err := reg.Select(c.Period, c.Facts.Admission)
			if err != nil {
				t.Fatal(err)
			}
			billed := map[string]money.Paise{}
			for _, l := range c.Facts.Lines {
				billed[l.ID] = l.Amount
			}
			adjusted := map[string]money.Paise{}
			for _, a := range c.Want.Adjustments {
				if _, ok := billed[a.Line]; !ok {
					t.Errorf("adjustment for unknown line %s", a.Line)
				}
				if _, ok := pack.Rule(a.Rule); !ok {
					t.Errorf("adjustment cites unknown rule %s", a.Rule)
				}
				if a.Amount <= 0 {
					t.Errorf("adjustment %s %s is %d; only non-zero adjustments are listed", a.Line, a.Rule, a.Amount)
				}
				adjusted[a.Line] += a.Amount
			}
			var total money.Paise
			var ids []string
			for _, l := range c.Want.Lines {
				ids = append(ids, l.ID)
				b, ok := billed[l.ID]
				switch {
				case !ok:
					t.Errorf("payable for unknown line %s", l.ID)
				case l.Payable < 0 || l.Payable > b:
					t.Errorf("%s pays %d of %d", l.ID, l.Payable, b)
				case b-l.Payable != adjusted[l.ID]:
					t.Errorf("%s: billed %d less payable %d is %d, but its adjustments add to %d",
						l.ID, b, l.Payable, b-l.Payable, adjusted[l.ID])
				}
				total += l.Payable
			}
			var factIDs []string
			for _, l := range c.Facts.Lines {
				factIDs = append(factIDs, l.ID)
			}
			slices.Sort(ids)
			slices.Sort(factIDs)
			if !slices.Equal(ids, slices.Compact(slices.Clone(ids))) || !slices.Equal(ids, factIDs) {
				t.Errorf("expected payables for %v, want one for each of %v", ids, factIDs)
			}
			if total != c.Want.ClaimPayable {
				t.Errorf("lines add up to %d, claim payable is %d", total, c.Want.ClaimPayable)
			}
			if c.Want.Ledger.Paid != c.Want.ClaimPayable {
				t.Errorf("ledger paid %d, want the payable %d (Definition 10)", c.Want.Ledger.Paid, c.Want.ClaimPayable)
			}
			claimFlags := map[rules.FlagCode]bool{}
			for _, f := range c.Want.Flags {
				switch {
				case !slices.Contains(rules.FlagCodes(), f.Code):
					t.Errorf("unknown flag %s", f.Code)
				case f.Code.AboutLine() != (f.Line != ""):
					t.Errorf("flag %s line %q: about a line is %t", f.Code, f.Line, f.Code.AboutLine())
				case f.Code.HasFigure() != f.HasFigure:
					t.Errorf("flag %s: has a figure is %t", f.Code, f.Code.HasFigure())
				case !f.Code.AboutLine() && claimFlags[f.Code]:
					t.Errorf("claim-level flag %s appears twice", f.Code)
				}
				claimFlags[f.Code] = true
			}
		})
	}
}

// TestGoldenCSV compares the figures checked in the spreadsheet with the
// case files. Any difference fails; never edit either side to make it pass.
func TestGoldenCSV(t *testing.T) {
	cases := loadCases(t)
	byCase := checked(t)
	if byCase == nil {
		t.Logf("no %s yet, so all %d cases are unchecked", engine.CSVPath, len(cases))
		return
	}
	known := map[string]engine.Case{}
	for _, c := range cases {
		known[c.ID] = c
	}
	var ids []string
	for id := range byCase {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		rows := byCase[id]
		c, ok := known[id]
		if !ok {
			t.Errorf("%s has rows for %s, which isn't a case", engine.CSVPath, id)
			continue
		}
		want := engine.ExpectedRows(c)
		if !slices.Equal(rows, want) {
			t.Errorf("%s: the CSV and the case file differ:\n csv  %v\n case %v", id, rows, want)
		}
		// The identity, on the CSV's own rows: billed less line payable
		// equals the line's adjustments.
		payable := map[string]int64{}
		adjusted := map[string]int64{}
		for _, r := range rows {
			switch r.Kind {
			case engine.KindLinePayable:
				payable[r.LineID] = r.Paise
			case engine.KindAdjustment:
				adjusted[r.LineID] += r.Paise
			}
		}
		for _, l := range c.Facts.Lines {
			if int64(l.Amount)-payable[l.ID] != adjusted[l.ID] {
				t.Errorf("%s %s: billed %d less payable %d isn't its adjustments, %d",
					id, l.ID, l.Amount, payable[l.ID], adjusted[l.ID])
			}
		}
	}
}

// TestGoldenEngine runs every case through the engine and reports it as
// pass, fail or pending, with each differing figure. A checked case that
// fails fails the test. Until the steps are coded, a case is pending: its
// figures from coded steps match, but it needs a step that isn't coded yet.
func TestGoldenEngine(t *testing.T) {
	reg := registry(t)
	byCase := checked(t)
	for _, c := range loadCases(t) {
		rep := engine.Run(c, reg)
		state := "unchecked"
		if len(byCase[c.ID]) > 0 {
			state = "checked"
		}
		var b strings.Builder
		for _, d := range rep.Diffs {
			when := "compared"
			if !d.Compared {
				when = "not yet"
			}
			b.WriteString("\n    " + when + "  " + d.Key + ": want " + d.Want + ", got " + d.Got)
		}
		msg := c.ID + ": " + state + ", " + string(rep.Status)
		if rep.Err != nil {
			msg += ": " + rep.Err.Error()
		} else if len(rep.Diffs) > 0 {
			msg += ", " + plural(len(rep.Diffs), "figure differs", "figures differ") + " from the full expectation"
		}
		if state == "checked" && rep.Status == engine.StatusFail {
			t.Error(msg + b.String())
		} else {
			t.Log(msg + b.String())
		}
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func TestParseCSV(t *testing.T) {
	const header = "case_id,kind,line_id,code,paise\n"
	tests := []struct {
		name    string
		csv     string
		wantErr string
		rows    int
	}{
		{name: "rows", csv: header + "a,claim_payable,,,100\na,line_payable,L01,,100\na,adjustment,L01,co-payment,5\n" +
			"a,flag,,bill_discount,7\na,flag,L01,undated_line,\n", rows: 5},
		{name: "byte-order mark and CRLF", csv: "\xef\xbb\xbf" + strings.ReplaceAll(header+"a,claim_payable,,,100\n", "\n", "\r\n"), rows: 1},
		{name: "header only", csv: header},
		{name: "wrong header", csv: "case,kind,line,code,paise\n", wantErr: "header"},
		{name: "unknown kind", csv: header + "a,payable,,,1\n", wantErr: "kind"},
		{name: "decimal figure", csv: header + "a,claim_payable,,,1.5\n", wantErr: "non-negative integer"},
		{name: "negative figure", csv: header + "a,claim_payable,,,-1\n", wantErr: "non-negative integer"},
		{name: "leading zero", csv: header + "a,claim_payable,,,01\n", wantErr: "non-negative integer"},
		{name: "line payable without a line", csv: header + "a,line_payable,,,1\n", wantErr: "needs a line_id"},
		{name: "claim payable with a line", csv: header + "a,claim_payable,L01,,1\n", wantErr: "has no line_id"},
		{name: "adjustment without a rule", csv: header + "a,adjustment,L01,,1\n", wantErr: "needs a code"},
		{name: "zero adjustment", csv: header + "a,adjustment,L01,co-payment,0\n", wantErr: "zero adjustment"},
		{name: "unknown flag", csv: header + "a,flag,,no_such_flag,\n", wantErr: "flag code"},
		{name: "line flag without a line", csv: header + "a,flag,,undated_line,\n", wantErr: "needs a line_id"},
		{name: "flag figure missing", csv: header + "a,flag,,bill_discount,\n", wantErr: "needs a paise"},
		{name: "flag figure where none goes", csv: header + "a,flag,,proposed_denial,0\n", wantErr: "has no paise"},
		{name: "repeated row", csv: header + "a,claim_payable,,,1\na,claim_payable,,,2\n", wantErr: "repeats line 2"},
		{name: "no case", csv: header + ",claim_payable,,,1\n", wantErr: "no case_id"},
		{name: "short row", csv: header + "a,claim_payable,,\n", wantErr: "wrong number of fields"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.ParseCSV([]byte(tt.csv))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for _, rows := range got {
				n += len(rows)
			}
			if n != tt.rows {
				t.Errorf("%d rows, want %d", n, tt.rows)
			}
		})
	}
}
