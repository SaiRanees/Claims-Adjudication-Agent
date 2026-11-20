package rules

import (
	"errors"
	"fmt"
)

// ErrNoPackGiven means Adjudicate was called without a pack.
var ErrNoPackGiven = errors.New("rules: no pack")

// Implemented reports whether the engine codes a step yet. A step it doesn't
// code passes every line through unchanged. The golden runner compares only
// what coded steps produce, so a checked case can wait for its steps without
// failing (week 2, session 5 codes eligibility).
func Implemented(s StepName) bool {
	switch s {
	case StepDropListedItems, StepSetAsideUnmodelledLines, StepApplyDailyCaps, StepApplyProportionateDeduction,
		StepApplySubLimits, StepCapAtCoverLeft, StepTakeCoPayment, StepTakeBillDiscount:
		return true
	case StepCheckEligibility:
		return false
	}
	return false
}

// Adjudicate computes a claim's payable under pack. It is pure: the same
// pack, facts and cover always give the same result, whatever order the
// lines come in.
//
// It runs the steps in the order the pack declares. Every step is coded
// except eligibility, which passes every tranche until week 2, session 5.
func Adjudicate(pack *Pack, facts Facts, cover Cover) (Result, error) {
	if pack == nil {
		return Result{}, ErrNoPackGiven
	}
	if err := checkInput(facts, cover); err != nil {
		return Result{}, err
	}
	c, err := newClaim(pack, facts, cover)
	if err != nil {
		return Result{}, err
	}
	for _, s := range pack.Steps {
		if err := c.run(s); err != nil {
			return Result{}, fmt.Errorf("rules: %s: %w", s.Name, err)
		}
	}
	if err := c.finish(); err != nil {
		return Result{}, fmt.Errorf("rules: %w", err)
	}
	return c.result()
}

func (c *claim) run(s Step) error {
	switch s.Name {
	case StepDropListedItems:
		return c.dropListedItems(s)
	case StepSetAsideUnmodelledLines:
		return c.setAsideUnmodelledLines(s)
	case StepApplyDailyCaps:
		return c.applyDailyCaps(s)
	case StepApplyProportionateDeduction:
		return c.applyProportionateDeduction(s)
	case StepApplySubLimits:
		return c.applySubLimits(s)
	case StepCapAtCoverLeft:
		return c.capAtCoverLeft(s)
	case StepTakeCoPayment:
		return c.takeCoPayment(s)
	case StepTakeBillDiscount:
		return c.takeBillDiscount(s)
	case StepCheckEligibility:
		// Not coded yet: every tranche counts.
		return nil
	}
	return fmt.Errorf("unknown step %q", s.Name)
}
