package rules

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// claim is a claim being adjudicated: its lines in ID order, what each still
// pays, and what the steps have recorded so far. Lines are worked in ID
// order, so the input order never moves a paisa, and nothing on the result
// path ranges over a map.
type claim struct {
	pack  *Pack
	facts Facts
	cover Cover

	lines  []Line
	amount []money.Paise // what each line pays so far
	// seg is the index of the segment each line's date falls in. An undated
	// line in a stay with one segment is in it; an undated line in a longer
	// stay, or one dated outside the stay, has -1.
	seg []int
	// bucket is the daily-cap bucket each line's category belongs to, and
	// inBucket whether it belongs to one.
	bucket   []SegmentKind
	inBucket []bool

	adjustments []Adjustment
	flags       []Flag
	// buckets holds each segment's bucket figures once the daily caps have
	// run; proportionate deduction reads them.
	buckets []bucket
}

func newClaim(pack *Pack, f Facts, cv Cover) (*claim, error) {
	caps, ok := pack.Step(StepApplyDailyCaps)
	if !ok {
		return nil, fmt.Errorf("rules: pack %s has no %s step", pack.ID, StepApplyDailyCaps)
	}
	order := make([]int, len(f.Lines))
	for i := range order {
		order[i] = i
	}
	slices.SortFunc(order, func(a, b int) int { return strings.Compare(f.Lines[a].ID, f.Lines[b].ID) })

	n := len(f.Lines)
	c := &claim{
		pack: pack, facts: f, cover: cv,
		lines: make([]Line, n), amount: make([]money.Paise, n), seg: make([]int, n),
		bucket: make([]SegmentKind, n), inBucket: make([]bool, n),
	}
	for k, i := range order {
		l := f.Lines[i]
		c.lines[k], c.amount[k] = l, l.Amount
		for _, r := range caps.Rules {
			if slices.Contains(r.Categories, l.Category) {
				c.bucket[k], c.inBucket[k] = SegmentKind(r.AppliesTo), true
			}
		}
		// A bucket line's date picks its segment, so it needs one when
		// there is more than one to pick from.
		if c.inBucket[k] && l.ServiceDate.IsZero() && len(f.Segments) > 1 {
			return nil, bad(fmt.Sprintf("Facts.Lines[%d].ServiceDate", i),
				"a room or ICU line needs a date when the stay has more than one segment")
		}
		c.seg[k] = segmentOf(f, l.ServiceDate)
	}
	return c, nil
}

// segmentOf returns the index of the segment d falls in, or -1.
func segmentOf(f Facts, d Date) int {
	if d.IsZero() {
		if len(f.Segments) == 1 {
			return 0
		}
		return -1
	}
	for i, s := range f.Segments {
		if !d.Before(s.From) && !d.After(s.To) {
			return i
		}
	}
	return -1
}

// sumInsured is the eligible sum insured. Until week 2, session 5 codes the
// waiting periods, every tranche counts.
func (c *claim) sumInsured() (money.Paise, error) {
	sums := make([]money.Paise, len(c.cover.Tranches))
	for i, t := range c.cover.Tranches {
		sums[i] = t.SumInsured
	}
	return money.Sum(sums...)
}

// deduct takes amount off line i under rule r, and adds the adjustment to
// adjs. A zero amount records nothing.
func (c *claim) deduct(adjs *[]Adjustment, i int, r Rule, amount money.Paise, inputs ...Input) error {
	if amount == 0 {
		return nil
	}
	left, err := money.Sub(c.amount[i], amount)
	if err != nil {
		return fmt.Errorf("%s on %s: %w", r.ID, c.lines[i].ID, err)
	}
	c.amount[i] = left
	*adjs = append(*adjs, Adjustment{LineID: c.lines[i].ID, RuleID: r.ID, Clause: r.Clause, Inputs: inputs, Amount: amount})
	return nil
}

// record adds one step's adjustments to the result, by line ID. The sort is
// stable, so a line with two adjustments in a step keeps the order they
// applied in.
func (c *claim) record(adjs []Adjustment) {
	slices.SortStableFunc(adjs, func(a, b Adjustment) int { return strings.Compare(a.LineID, b.LineID) })
	c.adjustments = append(c.adjustments, adjs...)
}

// flagLine raises a flag about line i, once.
func (c *claim) flagLine(code FlagCode, i int) {
	id := c.lines[i].ID
	if !slices.ContainsFunc(c.flags, func(f Flag) bool { return f.Code == code && f.LineID == id }) {
		c.flags = append(c.flags, Flag{Code: code, LineID: id})
	}
}

// flagClaim raises a claim-level flag, at most once per claim.
func (c *claim) flagClaim(code FlagCode, figure money.Paise) {
	if !slices.ContainsFunc(c.flags, func(f Flag) bool { return f.Code == code && f.LineID == "" }) {
		c.flags = append(c.flags, Flag{Code: code, Figure: figure, HasFigure: code.HasFigure()})
	}
}

func (c *claim) result() (Result, error) {
	res := Result{Pack: c.pack.Identity(), Lines: make([]LineResult, len(c.lines)), Adjustments: c.adjustments}
	billed := make([]money.Paise, len(c.lines))
	for i, l := range c.lines {
		res.Lines[i] = LineResult{ID: l.ID, Billed: l.Amount, Payable: c.amount[i]}
		billed[i] = l.Amount
		// Until the sub-limits are coded, a treatment's lines are admitted
		// at what they pay so far.
		switch l.Treatment.Kind {
		case TreatmentCataract:
			if l.Treatment.Eye == EyeLeft {
				res.Ledger.CataractUsed.Left += c.amount[i]
			} else {
				res.Ledger.CataractUsed.Right += c.amount[i]
			}
		case TreatmentModernTreatment:
			res.Ledger.ModernTreatmentUsed += c.amount[i]
		case TreatmentNone:
		}
	}
	var err error
	// checkInput has already checked that the bill's total fits, and no
	// line pays more than it billed.
	if res.Billed, err = money.Sum(billed...); err != nil {
		return Result{}, err
	}
	if res.Payable, err = money.Sum(c.amount...); err != nil {
		return Result{}, err
	}
	res.Ledger.Paid = res.Payable

	codes := FlagCodes()
	res.Flags = slices.Clone(c.flags)
	slices.SortFunc(res.Flags, func(a, b Flag) int {
		return cmp.Or(
			cmp.Compare(slices.Index(codes, a.Code), slices.Index(codes, b.Code)),
			strings.Compare(a.LineID, b.LineID),
		)
	})
	return res, nil
}
