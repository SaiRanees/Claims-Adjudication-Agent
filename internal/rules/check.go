package rules

import (
	"errors"
	"fmt"
	"math/bits"
	"slices"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// ErrInvalidInput means the facts or the cover break a rule every claim must
// meet. Bad input is an error; a flag is for valid input the engine can't
// decide. It comes wrapped in an *InputError naming the field.
var ErrInvalidInput = errors.New("rules: invalid input")

// InputError is invalid input at one field, such as Facts.Lines[2].Amount.
type InputError struct {
	Field  string
	Detail string
}

func (e *InputError) Error() string {
	return fmt.Sprintf("rules: invalid input: %s: %s", e.Field, e.Detail)
}

func (e *InputError) Unwrap() error { return ErrInvalidInput }

func bad(field, format string, args ...any) error {
	return &InputError{Field: field, Detail: fmt.Sprintf(format, args...)}
}

// checkInput checks the facts and the cover before any step runs.
func checkInput(f Facts, c Cover) error {
	if err := checkStay(f); err != nil {
		return err
	}
	if !slices.Contains(answers(), f.DifferentialBilling) {
		return bad("Facts.DifferentialBilling", "%q isn't yes, no or unknown", f.DifferentialBilling)
	}
	if f.BillDiscount < 0 {
		return bad("Facts.BillDiscount", "%d is below zero", f.BillDiscount)
	}
	if err := checkLines(f.Lines); err != nil {
		return err
	}
	if err := checkCondition(f.Condition); err != nil {
		return err
	}
	return checkCover(c)
}

// checkStay checks that the segments tile the stay: inclusive ranges in date
// order, each starting the day after the last ended, from admission to
// discharge. A same-day stay is one segment of one day.
func checkStay(f Facts) error {
	switch {
	case f.Admission.IsZero():
		return bad("Facts.Admission", "missing")
	case f.Discharge.IsZero():
		return bad("Facts.Discharge", "missing")
	case f.Discharge.Before(f.Admission):
		return bad("Facts.Discharge", "%s is before the admission on %s", f.Discharge, f.Admission)
	case len(f.Segments) == 0:
		return bad("Facts.Segments", "the stay has no segments")
	}
	next := f.Admission
	for i, s := range f.Segments {
		field := fmt.Sprintf("Facts.Segments[%d]", i)
		switch {
		case !slices.Contains(segmentKinds(), s.Kind):
			return bad(field+".Kind", "%q isn't room or icu", s.Kind)
		case s.From.IsZero() || s.To.IsZero():
			return bad(field, "missing a date")
		case s.From != next:
			return bad(field+".From", "%s, want %s, so the segments tile the stay", s.From, next)
		case s.To.Before(s.From):
			return bad(field+".To", "%s is before %s", s.To, s.From)
		case s.To.After(f.Discharge):
			return bad(field+".To", "%s is after the discharge on %s", s.To, f.Discharge)
		}
		if s.To == f.Discharge {
			if i != len(f.Segments)-1 {
				return bad(fmt.Sprintf("Facts.Segments[%d]", i+1), "starts after the discharge on %s", f.Discharge)
			}
			return nil
		}
		var err error
		if next, err = s.To.AddDays(1); err != nil {
			return bad(field+".To", "%v", err)
		}
	}
	return bad(fmt.Sprintf("Facts.Segments[%d].To", len(f.Segments)-1), "the segments end before the discharge on %s", f.Discharge)
}

func checkLines(lines []Line) error {
	if len(lines) == 0 {
		return bad("Facts.Lines", "the bill has no lines")
	}
	seen := make(map[string]bool, len(lines)) // membership only; never ranged over
	amounts := make([]money.Paise, len(lines))
	for i, l := range lines {
		field := fmt.Sprintf("Facts.Lines[%d]", i)
		switch {
		case l.ID == "":
			return bad(field+".ID", "missing")
		case seen[l.ID]:
			return bad(field+".ID", "%q is used twice", l.ID)
		case !slices.Contains(Categories(), l.Category):
			return bad(field+".Category", "%q isn't a category", l.Category)
		case l.Quantity < 1:
			return bad(field+".Quantity", "%d is below one", l.Quantity)
		case l.UnitPrice < 0:
			return bad(field+".UnitPrice", "%d is below zero", l.UnitPrice)
		case l.Amount < 0:
			return bad(field+".Amount", "%d is below zero", l.Amount)
		case l.ItemList != "" && !slices.Contains(Targets(StepDropListedItems), l.ItemList):
			return bad(field+".ItemList", "%q isn't an Annexure A list", l.ItemList)
		case !slices.Contains(treatmentKinds(), l.Treatment.Kind):
			return bad(field+".Treatment.Kind", "%q isn't a treatment with a sub-limit", l.Treatment.Kind)
		case l.Treatment.Kind == TreatmentCataract && l.Treatment.Eye != EyeLeft && l.Treatment.Eye != EyeRight:
			return bad(field+".Treatment.Eye", "a cataract line needs left or right, got %q", l.Treatment.Eye)
		case l.Treatment.Kind != TreatmentCataract && l.Treatment.Eye != EyeNone:
			return bad(field+".Treatment.Eye", "only a cataract line has an eye")
		}
		hi, lo := bits.Mul64(uint64(l.Quantity), uint64(l.UnitPrice))
		if hi != 0 || lo != uint64(l.Amount) {
			return bad(field+".Amount", "%d isn't the quantity %d × the unit price %d", l.Amount, l.Quantity, l.UnitPrice)
		}
		seen[l.ID] = true
		amounts[i] = l.Amount
	}
	if _, err := money.Sum(amounts...); err != nil {
		return bad("Facts.Lines", "the bill's total: %v", err)
	}
	return nil
}

func checkCondition(c Condition) error {
	if !slices.Contains(listedGroups(), c.ListedGroup) {
		return bad("Facts.Condition.ListedGroup", "%q isn't an Excl02 group", c.ListedGroup)
	}
	return nil
}

func checkCover(c Cover) error {
	if len(c.Tranches) == 0 {
		return bad("Cover.Tranches", "the cover has no sum insured")
	}
	sums := make([]money.Paise, len(c.Tranches))
	for i, t := range c.Tranches {
		field := fmt.Sprintf("Cover.Tranches[%d]", i)
		switch {
		case t.SumInsured < 1:
			return bad(field+".SumInsured", "%d is below one paisa", t.SumInsured)
		case t.ContinuousSince.IsZero():
			return bad(field+".ContinuousSince", "missing")
		}
		sums[i] = t.SumInsured
	}
	amounts := []struct {
		field string
		value money.Paise
	}{
		{"Cover.CumulativeBonus", c.CumulativeBonus},
		{"Cover.PaidThisYear", c.PaidThisYear},
		{"Cover.CataractUsed.Left", c.CataractUsed.Left},
		{"Cover.CataractUsed.Right", c.CataractUsed.Right},
		{"Cover.ModernTreatmentUsed", c.ModernTreatmentUsed},
	}
	for _, a := range amounts {
		if a.value < 0 {
			return bad(a.field, "%d is below zero", a.value)
		}
	}
	if _, err := money.Sum(append(sums, c.CumulativeBonus)...); err != nil {
		return bad("Cover", "the sum insured and bonus: %v", err)
	}
	return nil
}
