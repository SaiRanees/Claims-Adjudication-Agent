package engine

import (
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

// TestScope checks which figures the runner compares while the steps are
// coded one session at a time.
func TestScope(t *testing.T) {
	cases, err := Cases()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := rulepacks.Registry()
	if err != nil {
		t.Fatal(err)
	}
	worked := cases[slices.IndexFunc(cases, func(c Case) bool { return c.ID == "01-worked-example" })]
	pack, err := reg.Select(worked.Period, worked.Facts.Admission)
	if err != nil {
		t.Fatal(err)
	}
	coded := func(steps ...rules.StepName) func(rules.StepName) bool {
		return func(s rules.StepName) bool { return slices.Contains(steps, s) }
	}
	stub := coded()
	session3 := coded(rules.StepDropListedItems, rules.StepSetAsideUnmodelledLines,
		rules.StepApplyDailyCaps, rules.StepApplyProportionateDeduction)
	session4 := coded(rules.StepDropListedItems, rules.StepSetAsideUnmodelledLines,
		rules.StepApplyDailyCaps, rules.StepApplyProportionateDeduction, rules.StepApplySubLimits,
		rules.StepCapAtCoverLeft, rules.StepTakeCoPayment, rules.StepTakeBillDiscount)

	// An eligibility case: the same claim, expecting a tranche to be excluded.
	eligibility := worked
	eligibility.Want.Flags = []WantFlag{{Code: rules.FlagTrancheExcluded, Figure: 1, HasFigure: true}}

	row := func(kind, line, code string) Row { return Row{Kind: kind, LineID: line, Code: code} }
	tests := []struct {
		name        string
		c           Case
		implemented func(rules.StepName) bool
		row         Row
		want        bool
	}{
		{"stub: a step-1 adjustment", worked, stub, row(KindAdjustment, "L05", "annexure-a-list-ii"), false},
		{"stub: the claim payable", worked, stub, row(KindClaimPayable, "", ""), false},
		{"session 3: a daily cap", worked, session3, row(KindAdjustment, "L01", "daily-cap-room"), true},
		{"session 3: proportionate deduction", worked, session3, row(KindAdjustment, "L02", "proportionate-deduction-room"), true},
		{"session 3: a flag from step 3", worked, session3, row(KindFlag, "", string(rules.FlagDifferentialBillingUnknown)), true},
		{"session 3: the co-payment", worked, session3, row(KindAdjustment, "L01", "co-payment"), false},
		{"session 3: a line payable", worked, session3, row(KindLinePayable, "L01", ""), false},
		{"session 3: cover_exceeded", worked, session3, row(KindFlag, "", string(rules.FlagCoverExceeded)), false},
		{"session 4: the co-payment", worked, session4, row(KindAdjustment, "L01", "co-payment"), true},
		{"session 4: the claim payable", worked, session4, row(KindClaimPayable, "", ""), true},
		{"session 4: a rule the pack lacks", worked, session4, row(KindAdjustment, "L01", "no-such-rule"), true},
		{"session 4, eligibility case: a daily cap", eligibility, session4, row(KindAdjustment, "L01", "daily-cap-room"), false},
		{"session 4, eligibility case: the claim payable", eligibility, session4, row(KindClaimPayable, "", ""), false},
		{"all coded, eligibility case: the claim payable", eligibility, func(rules.StepName) bool { return true }, row(KindClaimPayable, "", ""), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scope(tt.c, pack, tt.implemented)(tt.row); got != tt.want {
				t.Errorf("compared = %t, want %t", got, tt.want)
			}
		})
	}
}
