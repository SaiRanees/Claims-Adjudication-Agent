package engine

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// The kinds of row, as golden-cases.csv names them.
const (
	KindLinePayable  = "line_payable"
	KindAdjustment   = "adjustment"
	KindClaimPayable = "claim_payable"
	KindFlag         = "flag"
)

// Row is one figure of a result, keyed as golden-cases.csv keys it.
type Row struct {
	Kind string
	// LineID is set for a line payable, an adjustment and a flag about one
	// line.
	LineID string
	// Code is the rule ID of an adjustment or the reason code of a flag.
	Code string
	// Paise is empty only for a flag without a figure.
	Paise    int64
	HasPaise bool
}

// Key is what makes a row unique within a case.
func (r Row) Key() string { return r.Kind + "|" + r.LineID + "|" + r.Code }

func (r Row) String() string {
	figure := "no figure"
	if r.HasPaise {
		figure = fmt.Sprintf("%d paise", r.Paise)
	}
	name := r.Kind
	if r.LineID != "" {
		name += " " + r.LineID
	}
	if r.Code != "" {
		name += " " + r.Code
	}
	return name + ": " + figure
}

func compareRows(a, b Row) int {
	return cmp.Or(
		strings.Compare(a.Kind, b.Kind),
		strings.Compare(a.LineID, b.LineID),
		strings.Compare(a.Code, b.Code),
		cmp.Compare(a.Paise, b.Paise),
	)
}

// ExpectedRows returns the rows a case expects, sorted, following the CSV's
// rules: one claim_payable row, a line_payable row for every line, a row for
// each non-zero adjustment and one for each flag.
func ExpectedRows(c Case) []Row {
	rows := []Row{{Kind: KindClaimPayable, Paise: int64(c.Want.ClaimPayable), HasPaise: true}}
	for _, l := range c.Want.Lines {
		rows = append(rows, Row{Kind: KindLinePayable, LineID: l.ID, Paise: int64(l.Payable), HasPaise: true})
	}
	for _, a := range c.Want.Adjustments {
		rows = append(rows, Row{Kind: KindAdjustment, LineID: a.Line, Code: a.Rule, Paise: int64(a.Amount), HasPaise: true})
	}
	for _, f := range c.Want.Flags {
		rows = append(rows, Row{Kind: KindFlag, LineID: f.Line, Code: string(f.Code), Paise: int64(f.Figure), HasPaise: f.HasFigure})
	}
	slices.SortFunc(rows, compareRows)
	return rows
}

// ResultRows returns an engine result's rows, sorted. Zero adjustments have
// no row.
func ResultRows(r rules.Result) []Row {
	rows := []Row{{Kind: KindClaimPayable, Paise: int64(r.Payable), HasPaise: true}}
	for _, l := range r.Lines {
		rows = append(rows, Row{Kind: KindLinePayable, LineID: l.ID, Paise: int64(l.Payable), HasPaise: true})
	}
	for _, a := range r.Adjustments {
		if a.Amount != 0 {
			rows = append(rows, Row{Kind: KindAdjustment, LineID: a.LineID, Code: a.RuleID, Paise: int64(a.Amount), HasPaise: true})
		}
	}
	for _, f := range r.Flags {
		rows = append(rows, Row{Kind: KindFlag, LineID: f.LineID, Code: string(f.Code), Paise: int64(f.Figure), HasPaise: f.HasFigure})
	}
	slices.SortFunc(rows, compareRows)
	return rows
}

// Status is a case's outcome against the engine.
type Status string

// The statuses.
const (
	// StatusPass: every figure matches.
	StatusPass Status = "pass"
	// StatusFail: a figure from a coded step differs.
	StatusFail Status = "fail"
	// StatusPending: every figure from coded steps matches, but the case
	// still needs a step the engine doesn't code yet.
	StatusPending Status = "pending"
)

// Diff is one figure that differs from the expectation.
type Diff struct {
	Key  string
	Want string // "missing" when the engine produced a row the case doesn't expect
	Got  string // "missing" when the engine didn't produce an expected row
	// Compared is false when the figure comes from a step the engine doesn't
	// code yet, or from one after it.
	Compared bool
}

// Report is a case's outcome.
type Report struct {
	Case   string
	Status Status
	Diffs  []Diff
	// Err is set when the engine couldn't run the case at all, which is a
	// failure.
	Err error
}

// Run selects the case's pack through its policy period, adjudicates it and
// compares the result with the expectation.
func Run(c Case, reg *rules.Registry) Report {
	rep := Report{Case: c.ID}
	pack, err := reg.Select(c.Period, c.Facts.Admission)
	if err == nil {
		var res rules.Result
		if res, err = rules.Adjudicate(pack, c.Facts, c.Cover); err == nil {
			rep.Diffs = diff(c, pack, res, rules.Implemented)
		}
	}
	switch {
	case err != nil:
		rep.Status, rep.Err = StatusFail, err
	case len(rep.Diffs) == 0:
		rep.Status = StatusPass
	case slices.ContainsFunc(rep.Diffs, func(d Diff) bool { return d.Compared }):
		rep.Status = StatusFail
	default:
		rep.Status = StatusPending
	}
	return rep
}

func diff(c Case, pack *rules.Pack, res rules.Result, implemented func(rules.StepName) bool) []Diff {
	compared := scope(c, pack, implemented)
	want := ExpectedRows(c)
	got := ResultRows(res)

	gotByKey := map[string]Row{}
	for _, r := range got {
		gotByKey[r.Key()] = r
	}
	wantByKey := map[string]Row{}
	for _, r := range want {
		wantByKey[r.Key()] = r
	}

	var diffs []Diff
	for _, w := range want { // sorted, so the diffs come out in order
		g, ok := gotByKey[w.Key()]
		switch {
		case !ok:
			diffs = append(diffs, Diff{Key: w.Key(), Want: w.String(), Got: "missing", Compared: compared(w)})
		case g != w:
			diffs = append(diffs, Diff{Key: w.Key(), Want: w.String(), Got: g.String(), Compared: compared(w)})
		}
	}
	for _, g := range got {
		if _, ok := wantByKey[g.Key()]; !ok {
			diffs = append(diffs, Diff{Key: g.Key(), Want: "missing", Got: g.String(), Compared: compared(g)})
		}
	}
	if res.Ledger != c.Want.Ledger {
		diffs = append(diffs, Diff{Key: "ledger", Want: fmt.Sprintf("%+v", c.Want.Ledger), Got: fmt.Sprintf("%+v", res.Ledger),
			Compared: compared(Row{Kind: KindClaimPayable})})
	}
	return diffs
}

// scope returns which rows can be compared yet. A row is compared once the
// engine codes the step it comes from and every step before that in the
// pack's order. Payables and the ledger come from the last step. Until week
// 2, session 5, eligibility passes every tranche through, which is what a
// case expects when it has no eligibility adjustment or tranche_excluded
// flag, so for such a case eligibility counts as coded.
func scope(c Case, pack *rules.Pack, implemented func(rules.StepName) bool) func(Row) bool {
	stepOf := map[string]rules.StepName{}
	for _, s := range pack.Steps {
		for _, r := range s.Rules {
			stepOf[r.ID] = s.Name
		}
	}
	needsEligibility := false
	for _, r := range ExpectedRows(c) {
		if (r.Kind == KindAdjustment && stepOf[r.Code] == rules.StepCheckEligibility) ||
			(r.Kind == KindFlag && rules.FlagCode(r.Code) == rules.FlagTrancheExcluded) {
			needsEligibility = true
		}
	}
	coded := func(through rules.StepName) bool {
		for _, s := range pack.Steps {
			if !implemented(s.Name) && (s.Name != rules.StepCheckEligibility || needsEligibility) {
				return false
			}
			if s.Name == through {
				return true
			}
		}
		return true
	}
	last := pack.Steps[len(pack.Steps)-1].Name
	return func(r Row) bool {
		switch r.Kind {
		case KindAdjustment:
			step, ok := stepOf[r.Code]
			if !ok {
				return true // a rule the pack doesn't have is always wrong
			}
			return coded(step)
		case KindFlag:
			return coded(rules.FlagCode(r.Code).Step())
		default:
			return coded(last)
		}
	}
}
