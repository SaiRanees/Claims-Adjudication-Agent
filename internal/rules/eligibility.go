package rules

import (
	"fmt"
	"slices"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// The targets of the eligibility rules that don't name a listed group.
const (
	targetAnyIllness  = "any-illness"
	targetPreExisting = "pre-existing"
)

// checkEligibility is the first step: each tranche of the sum insured is
// tested against the waiting periods from the date its continuous cover
// began, and only the tranches past every wait that applies count toward
// the eligible sum insured, which the later steps use. An excluded tranche
// raises tranche_excluded, carrying the total excluded.
//
// An onset before a tranche's cover began is outside its waits, or a
// pre-existing disease would be excluded under Excl03 however long it had
// been covered. On a condition not marked pre-existing, such an onset may
// be an undeclared pre-existing disease (Excl01), which the engine can't
// decide, so it raises onset_before_cover.
//
// When no tranche is eligible, every line's amount is deducted under the
// oldest tranche's binding wait, and the claim is a proposed denial. The
// engine never denies; a person decides.
func (c *claim) checkEligibility(s Step) error {
	var eligible, excluded []money.Paise
	oldest := -1
	var oldestRule Rule
	cond := c.facts.Condition
	for i, t := range c.cover.Tranches {
		if !cond.Onset.IsZero() && !cond.PreExisting && cond.Onset.Before(t.ContinuousSince) {
			c.flagClaim(FlagOnsetBeforeCover, 0)
		}
		r, out, err := c.bindingWait(s, t)
		if err != nil {
			return err
		}
		if !out {
			eligible = append(eligible, t.SumInsured)
			continue
		}
		excluded = append(excluded, t.SumInsured)
		if oldest < 0 || t.ContinuousSince.Before(c.cover.Tranches[oldest].ContinuousSince) {
			oldest, oldestRule = i, r
		}
	}
	sumInsured, err := money.Sum(eligible...)
	if err != nil {
		return err
	}
	c.eligible = &sumInsured
	if len(excluded) == 0 {
		return nil
	}
	out, err := money.Sum(excluded...)
	if err != nil {
		return err
	}
	c.flagClaim(FlagTrancheExcluded, out)
	if len(eligible) > 0 {
		return nil
	}

	c.allExcluded = true
	var adjs []Adjustment
	for i := range c.lines {
		amt := c.amount[i]
		if err := c.deduct(&adjs, i, oldestRule, amt, Input{"amount", int64(amt)}); err != nil {
			return err
		}
	}
	c.record(adjs)
	return nil
}

// bindingWait reports whether tranche t is still inside a wait that
// applies to the claim, and if so which: the one that ends last, ties going
// to the pack's order.
func (c *claim) bindingWait(s Step, t Tranche) (Rule, bool, error) {
	cond := c.facts.Condition
	var binding Rule
	var bindingEnd Date
	found := false
	for _, r := range s.Rules {
		var applies bool
		switch r.AppliesTo {
		case targetAnyIllness:
			applies = true
		case targetPreExisting:
			applies = cond.PreExisting
		default:
			if !slices.Contains(listedGroups(), r.AppliesTo) || r.AppliesTo == "" {
				return Rule{}, false, fmt.Errorf("rule %s applies to %q, which the engine doesn't know", r.ID, r.AppliesTo)
			}
			applies = cond.ListedGroup == r.AppliesTo
		}
		if !applies || (r.WaivedForAccident && cond.Accident) {
			continue
		}

		// Excl03 is about an illness within the first days: its onset
		// decides when the facts give one. The other waits are about the
		// treatment, so the admission decides.
		test := c.facts.Admission
		if r.AppliesTo == targetAnyIllness && !cond.Onset.IsZero() {
			test = cond.Onset
		}
		end, err := waitEnd(t.ContinuousSince, r.Days, r.Months)
		if err != nil {
			return Rule{}, false, fmt.Errorf("%s: %w", r.ID, err)
		}
		if test.Before(t.ContinuousSince) || test.After(end) {
			continue
		}
		if r.WaivedAfterMonths > 0 {
			// "Continuous Coverage for more than" that many months, judged
			// at the admission even when the onset decides the wait: an
			// illness that began in the first days stops being excluded
			// once it is treated after them (Excl03(ii)). An onset inside
			// the wait is never past the waiver itself.
			waiver, err := waitEnd(t.ContinuousSince, 0, r.WaivedAfterMonths)
			if err != nil {
				return Rule{}, false, fmt.Errorf("%s: %w", r.ID, err)
			}
			if c.facts.Admission.After(waiver) {
				continue
			}
		}
		if !found || end.After(bindingEnd) {
			binding, bindingEnd, found = r, end, true
		}
	}
	return binding, found, nil
}

// waitEnd is the last day of a wait of days or months from since: (since −
// 1 day) + the wait, as a policy schedule's "one year less a day" counts.
func waitEnd(since Date, days, months int64) (Date, error) {
	day, err := since.AddDays(-1)
	if err != nil {
		return Date{}, err
	}
	if days > 0 {
		return day.AddDays(int(days))
	}
	return day.AddMonths(int(months))
}
