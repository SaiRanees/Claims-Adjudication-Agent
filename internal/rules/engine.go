package rules

import (
	"errors"
	"fmt"
)

// ErrNoPackGiven means Adjudicate was called without a pack.
var ErrNoPackGiven = errors.New("rules: no pack")

// EngineVersion identifies the engine's code, as the pack's Identity
// identifies its rules. Every result records both, so replaying a claim can
// prove it ran the same rules through the same code. Change it with any
// change that could move a figure, an adjustment or a flag for the same
// pack, facts and cover.
const EngineVersion = "0.1.0"

// Implemented reports whether the engine codes a step. The golden runner
// compares only what coded steps produce, so while the steps were coded one
// session at a time, a checked case could wait for its steps without
// failing. Since week 2, session 5, every step is coded.
func Implemented(s StepName) bool {
	switch s {
	case StepCheckEligibility, StepDropListedItems, StepSetAsideUnmodelledLines, StepApplyDailyCaps,
		StepApplyProportionateDeduction, StepApplySubLimits, StepCapAtCoverLeft, StepTakeCoPayment,
		StepTakeBillDiscount:
		return true
	}
	return false
}

// Adjudicate computes a claim's payable under pack. It is pure: the same
// pack, facts and cover always give the same result, whatever order the
// lines come in.
//
// It runs the steps in the order the pack declares.
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
		return c.checkEligibility(s)
	}
	return fmt.Errorf("unknown step %q", s.Name)
}
