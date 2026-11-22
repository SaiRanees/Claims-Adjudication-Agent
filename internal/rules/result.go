package rules

import (
	"encoding/json"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// Result is the engine's answer for one claim. Every reduction is an
// adjustment, including a line's share of a claim-level reduction, so for
// every line, billed less payable equals the sum of its adjustments.
type Result struct {
	// Engine is the EngineVersion that computed the result, and Pack
	// identifies the pack it used, so a replay can prove it used the same
	// code and the same rules.
	Engine string
	Pack   Identity
	// Lines are sorted by ID.
	Lines []LineResult
	// Adjustments are in step order, then by line ID. None has a zero amount.
	Adjustments []Adjustment
	// Flags are in the order of FlagCodes, then by line ID.
	Flags   []Flag
	Billed  money.Paise
	Payable money.Paise
	Ledger  Ledger
}

// LineResult is one line's billed and payable amounts.
type LineResult struct {
	ID      string
	Billed  money.Paise
	Payable money.Paise
}

// Adjustment is one rule's reduction of one line. It is the explanation
// reviewers and letters show.
type Adjustment struct {
	LineID string
	RuleID string
	// Clause is the clause the rule cites, or empty for an unmodelled rule.
	Clause string
	// Inputs are the figures the rule used, such as the daily cap, in the
	// order the rule lists them.
	Inputs []Input
	Amount money.Paise
}

// Input is one named figure an adjustment used: an amount in paise, a rate
// in basis points or a count of days.
type Input struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// MarshalJSON writes the adjustment as M3 stores it, in the adjustments
// table and in audit details: IDs, reason codes and integer amounts only,
// never text from the documents. The keys follow the API's spelling. The
// clause ID is empty for an unmodelled rule, and the inputs are a list,
// empty when there are none.
func (a Adjustment) MarshalJSON() ([]byte, error) {
	inputs := a.Inputs
	if inputs == nil {
		inputs = []Input{}
	}
	return json.Marshal(struct {
		LineID      string      `json:"lineId"`
		RuleID      string      `json:"ruleId"`
		ClauseID    string      `json:"clauseId"`
		Inputs      []Input     `json:"inputs"`
		AmountPaise money.Paise `json:"amountPaise"`
	}{a.LineID, a.RuleID, a.Clause, inputs, a.Amount})
}

// Flag is a reason a person must look at the claim: valid input the engine
// can't decide, or a reading that is contested.
type Flag struct {
	Code FlagCode
	// LineID is set for a flag about one line, and empty for a claim-level
	// flag, which appears at most once per claim.
	LineID string
	// Figure is set only for the codes that carry one.
	Figure    money.Paise
	HasFigure bool
}

// MarshalJSON writes the flag as M3 stores it: its code, the line ID for a
// flag about one line, and the figure for a code that carries one, even
// when it is zero. A key that doesn't apply is left out.
func (f Flag) MarshalJSON() ([]byte, error) {
	out := struct {
		Code        FlagCode     `json:"code"`
		LineID      string       `json:"lineId,omitempty"`
		FigurePaise *money.Paise `json:"figurePaise,omitempty"`
	}{Code: f.Code, LineID: f.LineID}
	if f.HasFigure {
		figure := f.Figure
		out.FigurePaise = &figure
	}
	return json.Marshal(out)
}

// Ledger is what the claim adds to the policy year's balances. Its terms
// match Cover's.
type Ledger struct {
	// Paid is the payable: the sum insured and bonus the claim consumes
	// (Definition 10: the co-payment doesn't reduce the sum insured).
	Paid money.Paise
	// CataractUsed and ModernTreatmentUsed are what those lines were
	// admitted at before the co-payment, because the sub-limits apply
	// before it.
	CataractUsed        EyeAmounts
	ModernTreatmentUsed money.Paise
}

// FlagCode is a reason code. The list is closed.
type FlagCode string

// The flag codes.
const (
	FlagRoomDaysMismatch           FlagCode = "room_days_mismatch"
	FlagBucketSegmentMismatch      FlagCode = "bucket_segment_mismatch"
	FlagUndatedLine                FlagCode = "undated_line"
	FlagOutsideStay                FlagCode = "outside_stay"
	FlagPackageRate                FlagCode = "package_rate"
	FlagBillDiscount               FlagCode = "bill_discount"
	FlagDifferentialBillingUnknown FlagCode = "differential_billing_unknown"
	FlagNoRoomCharge               FlagCode = "no_room_charge"
	FlagICURatioScaling            FlagCode = "icu_ratio_scaling"
	FlagCoverExceeded              FlagCode = "cover_exceeded"
	FlagTrancheExcluded            FlagCode = "tranche_excluded"
	FlagOnsetBeforeCover           FlagCode = "onset_before_cover"
	FlagProposedDenial             FlagCode = "proposed_denial"
)

// FlagCodes returns every flag code, in the order results list them.
func FlagCodes() []FlagCode {
	return []FlagCode{
		FlagRoomDaysMismatch,
		FlagBucketSegmentMismatch,
		FlagUndatedLine,
		FlagOutsideStay,
		FlagPackageRate,
		FlagBillDiscount,
		FlagDifferentialBillingUnknown,
		FlagNoRoomCharge,
		FlagICURatioScaling,
		FlagCoverExceeded,
		FlagTrancheExcluded,
		FlagOnsetBeforeCover,
		FlagProposedDenial,
	}
}

// AboutLine reports whether the flag is about one line rather than the
// claim.
func (c FlagCode) AboutLine() bool {
	switch c {
	case FlagRoomDaysMismatch, FlagBucketSegmentMismatch, FlagUndatedLine,
		FlagOutsideStay, FlagPackageRate:
		return true
	case FlagBillDiscount, FlagDifferentialBillingUnknown, FlagNoRoomCharge,
		FlagICURatioScaling, FlagCoverExceeded, FlagTrancheExcluded, FlagOnsetBeforeCover, FlagProposedDenial:
		return false
	}
	return false
}

// HasFigure reports whether the flag carries a figure: the discount, the
// total ICU-ratio deduction, the co-pay-first payable, or the excluded sum
// insured.
func (c FlagCode) HasFigure() bool {
	switch c {
	case FlagBillDiscount, FlagICURatioScaling, FlagCoverExceeded, FlagTrancheExcluded:
		return true
	case FlagRoomDaysMismatch, FlagBucketSegmentMismatch, FlagUndatedLine, FlagOutsideStay,
		FlagPackageRate, FlagDifferentialBillingUnknown, FlagNoRoomCharge, FlagOnsetBeforeCover,
		FlagProposedDenial:
		return false
	}
	return false
}

// Step returns the last step the flag depends on: the step that raises it,
// or for a flag whose figure needs later steps, the latest of those.
func (c FlagCode) Step() StepName {
	switch c {
	case FlagTrancheExcluded, FlagOnsetBeforeCover:
		return StepCheckEligibility
	case FlagOutsideStay, FlagPackageRate:
		return StepSetAsideUnmodelledLines
	case FlagRoomDaysMismatch, FlagBucketSegmentMismatch:
		return StepApplyDailyCaps
	case FlagUndatedLine, FlagDifferentialBillingUnknown, FlagNoRoomCharge, FlagICURatioScaling:
		return StepApplyProportionateDeduction
	case FlagBillDiscount, FlagCoverExceeded, FlagProposedDenial:
		// cover_exceeded's figure is the co-pay-first payable after the
		// discount, and proposed_denial depends on the final payable.
		return StepTakeBillDiscount
	}
	return StepTakeBillDiscount
}
