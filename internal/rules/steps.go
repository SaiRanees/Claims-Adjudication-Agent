package rules

import (
	"cmp"
	"fmt"
	"math"
	"math/bits"
	"slices"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// The targets of the set-aside-unmodelled-lines step's rules.
const (
	targetPackageRate = "package-rate"
	targetOutsideStay = "outside-stay"
)

// ruleFor returns the step's rule for target. A parsed pack's rules cover
// every target of their step, so a miss means the pack wasn't parsed.
func ruleFor(s Step, target string) (Rule, error) {
	for _, r := range s.Rules {
		if r.AppliesTo == target {
			return r, nil
		}
	}
	return Rule{}, fmt.Errorf("step %s has no rule for %q", s.Name, target)
}

// dropListedItems is step 1: a line an Annexure A list matched pays nothing,
// and its adjustment cites the list.
func (c *claim) dropListedItems(s Step) error {
	var adjs []Adjustment
	for i, l := range c.lines {
		if l.ItemList == "" || c.amount[i] == 0 {
			continue
		}
		r, err := ruleFor(s, l.ItemList)
		if err != nil {
			return err
		}
		amt := c.amount[i]
		if err := c.deduct(&adjs, i, r, amt, Input{"amount", int64(amt)}); err != nil {
			return err
		}
	}
	c.record(adjs)
	return nil
}

// setAsideUnmodelledLines deducts what is left of each line the engine
// doesn't model: a line billed at a package rate, or dated outside the stay.
// Paying nothing is at or below every reading of such a line, and its flag
// sends the claim to a person. A line an Annexure list already zeroed keeps
// the list as its reason and raises nothing here. The rules apply in the
// pack's order, so a line that is both is deducted once, by the first.
func (c *claim) setAsideUnmodelledLines(s Step) error {
	var adjs []Adjustment
	for _, r := range s.Rules {
		var code FlagCode
		var applies func(Line) bool
		switch r.AppliesTo {
		case targetPackageRate:
			code, applies = FlagPackageRate, func(l Line) bool { return l.PackageRate }
		case targetOutsideStay:
			code, applies = FlagOutsideStay, c.outsideStay
		default:
			return fmt.Errorf("rule %s applies to %q, which the engine doesn't know", r.ID, r.AppliesTo)
		}
		for i, l := range c.lines {
			if !applies(l) || c.amount[i] == 0 {
				continue
			}
			amt := c.amount[i]
			if err := c.deduct(&adjs, i, r, amt, Input{"amount", int64(amt)}); err != nil {
				return err
			}
			c.flagLine(code, i)
			c.setAside = true
		}
	}
	c.record(adjs)
	return nil
}

func (c *claim) outsideStay(l Line) bool {
	d := l.ServiceDate
	return !d.IsZero() && (d.Before(c.facts.Admission) || d.After(c.facts.Discharge))
}

// bucket is one segment's daily-cap bucket, as proportionate deduction needs
// it. A segment whose bucket has no line left to pay has a zero billed
// total, and so no ratio.
type bucket struct {
	kind SegmentKind
	// billed is the bucket's total as it entered the daily caps, with each
	// line held to both caps at its billed amount.
	billed money.Paise
	// admissible is what the caps admit when the allowance counts the days
	// billed rather than the segment's calendar days: Note 2's admissible
	// rate per day, times the days billed. It is never more than billed.
	admissible money.Paise
}

// cut is a deduction the daily caps will make.
type cut struct {
	line   int
	rule   Rule
	amount money.Paise
	inputs []Input
}

// capPlan is what the daily caps do to a claim: the deductions, the flags
// about lines, and each segment's bucket.
type capPlan struct {
	cuts    []cut
	flags   []lineFlag
	buckets []bucket
}

type lineFlag struct {
	code FlagCode
	line int
}

// applyDailyCaps is step 2. Each segment's bucket is capped at the daily cap
// times the smaller of its days billed and the segment's calendar days.
func (c *claim) applyDailyCaps(s Step) error {
	p, err := c.planDailyCaps(s)
	if err != nil {
		return err
	}
	var adjs []Adjustment
	for _, k := range p.cuts {
		if err := c.deduct(&adjs, k.line, k.rule, k.amount, k.inputs...); err != nil {
			return err
		}
	}
	c.record(adjs)
	for _, f := range p.flags {
		c.flagLine(f.code, f.line)
	}
	c.buckets = p.buckets
	return nil
}

// planDailyCaps works out the daily caps from what each line pays so far,
// without changing it.
//
// A bucket line dated in a segment of the other kind (nursing on an ICU day,
// say) raises bucket_segment_mismatch and is held to both caps: first its
// own bucket's cap, then the segment's, whose bucket it joins at what its own
// cap left. A bucket's days billed are the days its lines cover together
// (daysCovered). room_days_mismatch is judged per category: when the days a
// category's lines cover together in a segment are neither its calendar
// days nor one fewer, each of those lines raises it.
func (c *claim) planDailyCaps(s Step) (capPlan, error) {
	var p capPlan
	sumInsured, err := c.sumInsured()
	if err != nil {
		return p, err
	}
	amt := slices.Clone(c.amount)
	for si, sg := range c.facts.Segments {
		days := int64(sg.From.DaysUntil(sg.To)) + 1
		var members []int
		for i := range c.lines {
			if c.inBucket[i] && c.seg[i] == si && amt[i] > 0 {
				members = append(members, i)
			}
		}
		b := bucket{kind: sg.Kind}
		if len(members) == 0 {
			p.buckets = append(p.buckets, b)
			continue
		}
		if b.billed, err = total(amt, members); err != nil {
			return p, err
		}
		for _, cat := range Categories() {
			var same []int
			for _, i := range members {
				if c.lines[i].Category == cat {
					same = append(same, i)
				}
			}
			if len(same) == 0 {
				continue
			}
			if d := c.daysCovered(sg, same); d != days && d != days-1 {
				for _, i := range same {
					p.flags = append(p.flags, lineFlag{FlagRoomDaysMismatch, i})
				}
			}
		}

		// Lines of the other kind meet their own bucket's cap first.
		var admissible []money.Paise
		var native []int
		for _, i := range members {
			if c.bucket[i] == sg.Kind {
				native = append(native, i)
			}
		}
		for _, kind := range segmentKinds() {
			if kind == sg.Kind {
				continue
			}
			var group []int
			for _, i := range members {
				if c.bucket[i] == kind {
					group = append(group, i)
					p.flags = append(p.flags, lineFlag{FlagBucketSegmentMismatch, i})
				}
			}
			if len(group) == 0 {
				continue
			}
			groupTotal, perDay, err := c.capGroup(&p, s, sg, amt, group, kind, sumInsured)
			if err != nil {
				return p, err
			}
			admissible = append(admissible, min(groupTotal, perDay))
		}
		nativeTotal, err := total(amt, native)
		if err != nil {
			return p, err
		}
		admissible = append(admissible, nativeTotal)

		// Then the segment's cap, over every line in its bucket.
		_, perDay, err := c.capGroup(&p, s, sg, amt, members, sg.Kind, sumInsured)
		if err != nil {
			return p, err
		}
		sum, err := money.Sum(admissible...)
		if err != nil {
			return p, err
		}
		b.admissible = min(sum, perDay)
		p.buckets = append(p.buckets, b)
	}
	return p, nil
}

// capGroup caps the lines in group, which belong to kind's bucket and are
// dated in segment sg, at its daily cap times the smaller of the days they
// cover together and the segment's calendar days. It spreads any reduction
// across them by amount, adds it to p's cuts and takes it off amt.
//
// It returns the group's total before the cap, and the daily cap times the
// days billed, which is what the ratio counts.
func (c *claim) capGroup(p *capPlan, s Step, sg Segment, amt []money.Paise, group []int, kind SegmentKind, sumInsured money.Paise) (groupTotal, perDay money.Paise, err error) {
	r, err := ruleFor(s, string(kind))
	if err != nil {
		return 0, 0, err
	}
	daily, err := limitOf(r.Limit, sumInsured)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", r.ID, err)
	}
	weights := make([]int64, len(group))
	for k, i := range group {
		weights[k] = int64(amt[i])
	}
	if groupTotal, err = total(amt, group); err != nil {
		return 0, 0, err
	}
	billedDays := c.daysCovered(sg, group)
	counted := min(billedDays, int64(sg.From.DaysUntil(sg.To))+1)
	allowance, err := money.MulDiv(daily, counted, 1)
	if err != nil {
		return 0, 0, fmt.Errorf("%s: %w", r.ID, err)
	}
	if perDay, err = money.MulDiv(daily, billedDays, 1); err != nil {
		return 0, 0, fmt.Errorf("%s: %w", r.ID, err)
	}
	if groupTotal > allowance {
		shares, err := money.Allocate(groupTotal-allowance, weights)
		if err != nil {
			return 0, 0, fmt.Errorf("%s: %w", r.ID, err)
		}
		inputs := []Input{
			{"daily_cap", int64(daily)}, {"days", counted}, {"bucket_total", int64(groupTotal)}, {"allowance", int64(allowance)},
		}
		for k, i := range group {
			if shares[k] > 0 {
				p.cuts = append(p.cuts, cut{line: i, rule: r, amount: shares[k], inputs: inputs})
				amt[i] -= shares[k]
			}
		}
	}
	return groupTotal, perDay, nil
}

// daysCovered counts the days the lines in idx, all dated in segment sg,
// cover together. Each line covers its quantity of days from its service
// date; an undated one, possible only in a stay with one segment, from the
// segment's first day. Days past the segment's end count, so billing more
// days than the segment has shows. The count saturates at math.MaxInt64.
func (c *claim) daysCovered(sg Segment, idx []int) int64 {
	type span struct{ from, to int64 } // inclusive, in days from sg.From
	spans := make([]span, 0, len(idx))
	for _, i := range idx {
		var from int64
		if d := c.lines[i].ServiceDate; !d.IsZero() {
			from = int64(sg.From.DaysUntil(d))
		}
		to := int64(math.MaxInt64)
		if q := c.lines[i].Quantity; q-1 <= math.MaxInt64-from {
			to = from + q - 1
		}
		spans = append(spans, span{from, to})
	}
	if len(spans) == 0 {
		return 0
	}
	slices.SortFunc(spans, func(a, b span) int { return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to)) })
	var days int64
	length := func(s span) int64 {
		if s.to-s.from == math.MaxInt64 {
			return math.MaxInt64
		}
		return s.to - s.from + 1
	}
	cur := spans[0]
	for _, s := range spans[1:] {
		if s.from <= cur.to || s.from-1 == cur.to {
			cur.to = max(cur.to, s.to)
			continue
		}
		days = saturatingAdd(days, length(cur))
		cur = s
	}
	return saturatingAdd(days, length(cur))
}

func saturatingAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// limitOf is a limit's value for a sum insured: the lower of its share and
// its ceiling, or whichever of the two it has.
func limitOf(l Limit, sumInsured money.Paise) (money.Paise, error) {
	if !l.HasShare {
		if !l.HasCeiling {
			return 0, fmt.Errorf("a limit with neither a share nor a ceiling")
		}
		return l.Ceiling, nil
	}
	share, err := money.Percent(sumInsured, l.Share)
	if err != nil {
		return 0, err
	}
	if l.HasCeiling {
		return min(share, l.Ceiling), nil
	}
	return share, nil
}

func total(amt []money.Paise, idx []int) (money.Paise, error) {
	parts := make([]money.Paise, len(idx))
	for k, i := range idx {
		parts[k] = amt[i]
	}
	return money.Sum(parts...)
}

// ratio is a segment's proportionate-deduction ratio, num ÷ den.
type ratio struct {
	num, den money.Paise
	rule     Rule
	ok       bool
}

// below reports whether r is a lower ratio than o.
func (r ratio) below(o ratio) bool {
	// num and den are non-negative paise, so the products fit in 128 bits.
	ahi, alo := bits.Mul64(uint64(r.num), uint64(o.den)) //nolint:gosec // both are paise, at least zero.
	bhi, blo := bits.Mul64(uint64(o.num), uint64(r.den)) //nolint:gosec // both are paise, at least zero.
	return ahi < bhi || (ahi == bhi && alo < blo)
}

// applyProportionateDeduction is step 3. Each scaled line is multiplied by
// its segment's ratio, rounded once. Note 3: a hospital without differential
// billing gets no scaling; when the facts don't say, the step follows the
// wording and raises differential_billing_unknown. A segment with no bucket
// line has no ratio and raises no_room_charge. A rule whose effect is
// no-scaling skips its segments and raises nothing. An undated line in a stay
// with more than one segment takes the lowest ratio in the stay and raises
// undated_line. Scaling by an ICU ratio raises icu_ratio_scaling, carrying
// the total it deducted (ADR 0010).
func (c *claim) applyProportionateDeduction(s Step) error {
	switch c.facts.DifferentialBilling {
	case AnswerNo:
		return nil
	case AnswerUnknown:
		c.flagClaim(FlagDifferentialBillingUnknown, 0)
	case AnswerYes:
	}
	buckets := c.buckets
	if buckets == nil {
		// A pack that scales before it caps: the ratio is the same, worked
		// out from what the lines pay now.
		caps, _ := c.pack.Step(StepApplyDailyCaps)
		p, err := c.planDailyCaps(caps)
		if err != nil {
			return err
		}
		buckets = p.buckets
	}

	ratios := make([]ratio, len(buckets))
	var lowest ratio
	for i, b := range buckets {
		r, err := ruleFor(s, string(b.kind))
		if err != nil {
			return err
		}
		switch r.Effect {
		case EffectNoScaling:
			continue
		case EffectScale:
		case EffectNotPayable, EffectRoomCharges, EffectProcedureCharges, EffectTreatmentCosts:
			return fmt.Errorf("rule %s has effect %q, which doesn't scale", r.ID, r.Effect)
		}
		if b.billed == 0 {
			c.flagClaim(FlagNoRoomCharge, 0)
			continue
		}
		ratios[i] = ratio{num: b.admissible, den: b.billed, rule: r, ok: true}
		if !lowest.ok || ratios[i].below(lowest) {
			lowest = ratios[i]
		}
	}

	var adjs []Adjustment
	var icu money.Paise
	for i, l := range c.lines {
		if !slices.Contains(s.ScaledCategories, l.Category) || c.amount[i] == 0 {
			continue
		}
		var rt ratio
		switch {
		case c.seg[i] >= 0:
			rt = ratios[c.seg[i]]
		case l.ServiceDate.IsZero():
			c.flagLine(FlagUndatedLine, i)
			rt = lowest
		}
		if !rt.ok || rt.num >= rt.den {
			continue
		}
		amt := c.amount[i]
		kept, err := money.MulDiv(amt, int64(rt.num), int64(rt.den))
		if err != nil {
			return fmt.Errorf("%s on %s: %w", rt.rule.ID, l.ID, err)
		}
		deducted := amt - kept
		inputs := []Input{{"admissible", int64(rt.num)}, {"billed", int64(rt.den)}, {"amount", int64(amt)}}
		if err := c.deduct(&adjs, i, rt.rule, deducted, inputs...); err != nil {
			return err
		}
		if rt.rule.AppliesTo == string(SegmentICU) {
			if icu, err = money.Add(icu, deducted); err != nil {
				return err
			}
		}
	}
	c.record(adjs)
	if icu > 0 {
		c.flagClaim(FlagICURatioScaling, icu)
	}
	return nil
}
