package rules

import (
	"fmt"
	"slices"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// The targets of the sub-limit rules, and of the claim-level rules.
const (
	targetCataract        = "cataract"
	targetModernTreatment = "modern-treatment"
	targetRoadAmbulance   = "road-ambulance"
	targetClaim           = "claim"
)

// applySubLimits is step 4. Each sub-limit caps the lines the facts tie to
// its treatment, less what this policy year has already used of it. A
// percentage is of the eligible sum insured, without the cumulative bonus.
func (c *claim) applySubLimits(s Step) error {
	sumInsured, err := c.sumInsured()
	if err != nil {
		return err
	}
	var adjs []Adjustment
	for _, r := range s.Rules {
		limit, err := limitOf(r.Limit, sumInsured)
		if err != nil {
			return fmt.Errorf("%s: %w", r.ID, err)
		}
		switch r.AppliesTo {
		case targetCataract:
			// Per eye per policy year.
			for _, eye := range []Eye{EyeLeft, EyeRight} {
				used := c.cover.CataractUsed.Left
				if eye == EyeRight {
					used = c.cover.CataractUsed.Right
				}
				group := c.linesWhere(func(l Line) bool {
					return l.Treatment.Kind == TreatmentCataract && l.Treatment.Eye == eye
				})
				if err := c.limitGroup(&adjs, r, group, limit, used); err != nil {
					return err
				}
			}
		case targetModernTreatment:
			group := c.linesWhere(func(l Line) bool { return l.Treatment.Kind == TreatmentModernTreatment })
			if err := c.limitGroup(&adjs, r, group, limit, c.cover.ModernTreatmentUsed); err != nil {
				return err
			}
		case targetRoadAmbulance:
			// Per hospitalisation, so nothing is used before this claim.
			group := c.linesWhere(func(l Line) bool { return l.Category == CategoryRoadAmbulance })
			if err := c.limitGroup(&adjs, r, group, limit, 0); err != nil {
				return err
			}
		default:
			return fmt.Errorf("rule %s applies to %q, which the engine doesn't know", r.ID, r.AppliesTo)
		}
	}
	c.record(adjs)
	return nil
}

func (c *claim) linesWhere(match func(Line) bool) []int {
	var idx []int
	for i, l := range c.lines {
		if match(l) {
			idx = append(idx, i)
		}
	}
	return idx
}

// limitGroup caps the lines in group at what is left of limit after used,
// never below zero, spreading any reduction by amount.
func (c *claim) limitGroup(adjs *[]Adjustment, r Rule, group []int, limit, used money.Paise) error {
	if len(group) == 0 {
		return nil
	}
	var left money.Paise
	if used < limit {
		left = limit - used
	}
	tied, err := total(c.amount, group)
	if err != nil {
		return err
	}
	if tied <= left {
		return nil
	}
	return c.spread(adjs, r, group, tied-left,
		Input{"limit", int64(limit)}, Input{"used", int64(used)}, Input{"left", int64(left)}, Input{"tied_total", int64(tied)})
}

// spread deducts amount from the lines in idx, which are in ID order, split
// by what each pays so far with Allocate, under rule r. Nothing to spread
// records nothing; Allocate would fail with every weight zero.
func (c *claim) spread(adjs *[]Adjustment, r Rule, idx []int, amount money.Paise, inputs ...Input) error {
	if amount == 0 {
		return nil
	}
	weights := make([]int64, len(idx))
	for k, i := range idx {
		weights[k] = int64(c.amount[i])
	}
	shares, err := money.Allocate(amount, weights)
	if err != nil {
		return fmt.Errorf("%s: %w", r.ID, err)
	}
	for k, i := range idx {
		if err := c.deduct(adjs, i, r, shares[k], inputs...); err != nil {
			return err
		}
	}
	return nil
}

func (c *claim) allLines() []int {
	idx := make([]int, len(c.lines))
	for i := range idx {
		idx[i] = i
	}
	return idx
}

// capAtCoverLeft is step 5: the admissible total is capped at the cover
// left, the eligible sum insured plus the cumulative bonus granted for the
// year, less what has been paid this policy year, never below zero
// (ADR 0009). A claim it caps raises cover_exceeded once the discount is
// known (finish).
func (c *claim) capAtCoverLeft(s Step) error {
	r, err := ruleFor(s, targetClaim)
	if err != nil {
		return err
	}
	sumInsured, err := c.sumInsured()
	if err != nil {
		return err
	}
	cover, err := money.Add(sumInsured, c.cover.CumulativeBonus)
	if err != nil {
		return err
	}
	var left money.Paise
	if c.cover.PaidThisYear < cover {
		left = cover - c.cover.PaidThisYear
	}
	admissible, err := money.Sum(c.amount...)
	if err != nil {
		return err
	}
	if admissible <= left {
		return nil
	}
	c.exceeded = &coverExcess{admissible: admissible, left: left}
	var adjs []Adjustment
	if err := c.spread(&adjs, r, c.allLines(), admissible-left,
		Input{"cover_left", int64(left)}, Input{"admissible", int64(admissible)}); err != nil {
		return err
	}
	c.record(adjs)
	return nil
}

// coverExcess is what step 5 found when it capped a claim.
type coverExcess struct {
	admissible, left money.Paise
}

// takeCoPayment is step 6: the rule's rate of the capped amount, computed
// once with Percent and spread across the lines (ADR 0009). What the lines
// pay just before it is what the ledger counts as sub-limit use.
func (c *claim) takeCoPayment(s Step) error {
	r, err := ruleFor(s, targetClaim)
	if err != nil {
		return err
	}
	c.beforeCoPay = slices.Clone(c.amount)
	base, err := money.Sum(c.amount...)
	if err != nil {
		return err
	}
	coPay, err := money.Percent(base, r.Rate)
	if err != nil {
		return fmt.Errorf("%s: %w", r.ID, err)
	}
	var adjs []Adjustment
	if err := c.spread(&adjs, r, c.allLines(), coPay, Input{"rate_bp", int64(r.Rate)}, Input{"base", int64(base)}); err != nil {
		return err
	}
	c.record(adjs)
	return nil
}

// takeBillDiscount deducts the smaller of the bill's discount and the
// payable, spread across the lines, and raises bill_discount carrying the
// discount. Deducting it last is at or below every reading of where it
// fell; a discount was never "actually incurred" (Definition 26).
func (c *claim) takeBillDiscount(s Step) error {
	r, err := ruleFor(s, targetClaim)
	if err != nil {
		return err
	}
	payable, err := money.Sum(c.amount...)
	if err != nil {
		return err
	}
	c.beforeDiscount = &payable
	discount := c.facts.BillDiscount
	if discount == 0 {
		return nil
	}
	c.flagClaim(FlagBillDiscount, discount)
	var adjs []Adjustment
	if err := c.spread(&adjs, r, c.allLines(), min(discount, payable),
		Input{"discount", int64(discount)}, Input{"payable", int64(payable)}); err != nil {
		return err
	}
	c.record(adjs)
	return nil
}

// finish raises the flags that need the final payable.
//
// cover_exceeded carries the co-pay-first payable after the same discount:
// the admissible total less its co-payment, capped at the cover left, less
// the smaller of the discount and that (ADR 0009). It is never lower than
// the payable.
//
// proposed_denial means the policy makes the claim pay nothing: every
// tranche is excluded, or the payable is zero, no line was zeroed as
// unmodelled, and the discount didn't take it to zero. A claim that pays
// nothing only because of a line the engine couldn't read goes to review
// through that line's flag instead.
func (c *claim) finish() error {
	payable, err := money.Sum(c.amount...)
	if err != nil {
		return err
	}
	if c.exceeded != nil {
		figure, err := c.coPayFirst()
		if err != nil {
			return err
		}
		c.flagClaim(FlagCoverExceeded, figure)
	}
	beforeDiscount := payable
	if c.beforeDiscount != nil {
		beforeDiscount = *c.beforeDiscount
	}
	if c.allExcluded || (payable == 0 && !c.setAside && beforeDiscount == 0) {
		c.flagClaim(FlagProposedDenial, 0)
	}
	return nil
}

func (c *claim) coPayFirst() (money.Paise, error) {
	s, ok := c.pack.Step(StepTakeCoPayment)
	if !ok {
		return 0, fmt.Errorf("pack %s has no %s step", c.pack.ID, StepTakeCoPayment)
	}
	r, err := ruleFor(s, targetClaim)
	if err != nil {
		return 0, err
	}
	coPay, err := money.Percent(c.exceeded.admissible, r.Rate)
	if err != nil {
		return 0, err
	}
	paid := min(c.exceeded.admissible-coPay, c.exceeded.left)
	return paid - min(c.facts.BillDiscount, paid), nil
}
