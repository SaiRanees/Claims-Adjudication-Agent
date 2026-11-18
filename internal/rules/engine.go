package rules

import (
	"errors"
	"slices"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// ErrNoPackGiven means Adjudicate was called without a pack.
var ErrNoPackGiven = errors.New("rules: no pack")

// Implemented reports whether the engine codes a step yet. A step it doesn't
// code passes every line through unchanged. The golden runner compares only
// what coded steps produce, so a checked case can wait for its steps without
// failing (week 2, sessions 3 to 5 code them).
func Implemented(s StepName) bool {
	switch s {
	case StepCheckEligibility, StepDropListedItems, StepSetAsideUnmodelledLines,
		StepApplyDailyCaps, StepApplyProportionateDeduction, StepApplySubLimits,
		StepCapAtCoverLeft, StepTakeCoPayment, StepTakeBillDiscount:
		return false
	}
	return false
}

// Adjudicate computes a claim's payable under pack. It is pure: the same
// pack, facts and cover always give the same result, whatever order the
// lines come in.
//
// For now every step passes the lines through, so each line pays what it
// billed. Sessions 3 to 5 of week 2 code the steps.
func Adjudicate(pack *Pack, facts Facts, cover Cover) (Result, error) {
	if pack == nil {
		return Result{}, ErrNoPackGiven
	}
	if err := checkInput(facts, cover); err != nil {
		return Result{}, err
	}

	// Work in ID order, so the input order never moves a paisa.
	lines := slices.Clone(facts.Lines)
	slices.SortFunc(lines, func(a, b Line) int { return strings.Compare(a.ID, b.ID) })

	res := Result{Pack: pack.Identity(), Lines: make([]LineResult, len(lines))}
	amounts := make([]money.Paise, len(lines))
	for i, l := range lines {
		res.Lines[i] = LineResult{ID: l.ID, Billed: l.Amount, Payable: l.Amount}
		amounts[i] = l.Amount
		switch l.Treatment.Kind {
		case TreatmentCataract:
			if l.Treatment.Eye == EyeLeft {
				res.Ledger.CataractUsed.Left += l.Amount
			} else {
				res.Ledger.CataractUsed.Right += l.Amount
			}
		case TreatmentModernTreatment:
			res.Ledger.ModernTreatmentUsed += l.Amount
		case TreatmentNone:
		}
	}
	// checkInput has already checked that the total fits.
	total, err := money.Sum(amounts...)
	if err != nil {
		return Result{}, err
	}
	res.Billed, res.Payable, res.Ledger.Paid = total, total, total
	return res, nil
}
