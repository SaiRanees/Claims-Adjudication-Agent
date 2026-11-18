package rules

import (
	"crypto/sha256"
	"fmt"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// Pack is a parsed and validated rule pack: one policy wording's limits, the
// clauses they cite and the order of the steps. ParsePack builds one; nothing
// else should.
type Pack struct {
	ID        string
	Version   int64
	Wording   Wording
	Documents []Document
	Clauses   []Clause
	// Steps are in the order the engine runs them.
	Steps []Step

	sha256 [sha256.Size]byte
}

// Identity is what a result records about the pack it used. The SHA-256 is
// of the pack's bytes, so a replay can prove it used the same pack.
type Identity struct {
	ID      string
	Version int64
	SHA256  [sha256.Size]byte
}

// String formats the identity as id@version+sha256:hex.
func (i Identity) String() string {
	return fmt.Sprintf("%s@%d+sha256:%x", i.ID, i.Version, i.SHA256)
}

// Identity returns the pack's ID, version and SHA-256.
func (p *Pack) Identity() Identity {
	return Identity{ID: p.ID, Version: p.Version, SHA256: p.sha256}
}

// Rule returns the rule with the given ID.
func (p *Pack) Rule(id string) (Rule, bool) {
	for _, s := range p.Steps {
		for _, r := range s.Rules {
			if r.ID == id {
				return r, true
			}
		}
	}
	return Rule{}, false
}

// Clause returns the clause with the given ID.
func (p *Pack) Clause(id string) (Clause, bool) {
	for _, c := range p.Clauses {
		if c.ID == id {
			return c, true
		}
	}
	return Clause{}, false
}

// Wording identifies the policy wording a pack encodes.
type Wording struct {
	UIN       string
	ValidFrom Date
	// ValidUntil is the zero Date when the wording has no end date.
	ValidUntil Date
}

// Document is a source a clause comes from: the wording, or a circular.
type Document struct {
	ID    string
	Title string
	Pages int64
}

// Clause is a cited passage. LastPage is zero unless the passage runs over a
// page break.
type Clause struct {
	ID       string
	Document string
	Page     int64
	LastPage int64
	Title    string
}

// Step is one step of adjudication and its rules.
type Step struct {
	Name  StepName
	Rules []Rule

	// Set only for StepApplyProportionateDeduction.
	RatioBasis       RatioBasis
	ScaledCategories []Category
	ExemptCategories []Category
}

// Rule is one rule of a step. Only the fields its step uses are set.
type Rule struct {
	ID string
	// Clause is empty only when Unmodelled is true.
	Clause     string
	Unmodelled bool
	SeeAlso    []string
	// AppliesTo is one of the step's targets; see Targets.
	AppliesTo string

	// StepCheckEligibility: a waiting period of Days or Months (one of the
	// two), lifted after WaivedAfterMonths of continuous cover when non-zero.
	Days              int64
	Months            int64
	WaivedAfterMonths int64

	// StepDropListedItems.
	Effect ListEffect

	// StepApplyDailyCaps: the bucket's categories and its daily Limit.
	Categories []Category

	// StepApplyDailyCaps and StepApplySubLimits.
	Limit Limit
	// StepApplySubLimits.
	Per Period

	// StepTakeCoPayment.
	Rate money.BasisPoints
}

// Limit is the lower of a share of the eligible sum insured and a fixed
// ceiling. A sub-limit may have only one of the two; a daily cap has both.
type Limit struct {
	Share      money.BasisPoints
	HasShare   bool
	Ceiling    money.Paise
	HasCeiling bool
}

// StepName names a step. A pack lists every step exactly once.
type StepName string

// The steps, in the order architecture.md and ADR 0009 give them.
const (
	StepCheckEligibility            StepName = "check-eligibility"
	StepDropListedItems             StepName = "drop-listed-items"
	StepSetAsideUnmodelledLines     StepName = "set-aside-unmodelled-lines"
	StepApplyDailyCaps              StepName = "apply-daily-caps"
	StepApplyProportionateDeduction StepName = "apply-proportionate-deduction"
	StepApplySubLimits              StepName = "apply-sub-limits"
	StepCapAtCoverLeft              StepName = "cap-at-cover-left"
	StepTakeCoPayment               StepName = "take-co-payment"
	StepTakeBillDiscount            StepName = "take-bill-discount"
)

// StepNames returns every step name, in the order architecture.md and ADR
// 0009 give them. A pack may declare another order.
func StepNames() []StepName {
	return []StepName{
		StepCheckEligibility,
		StepDropListedItems,
		StepSetAsideUnmodelledLines,
		StepApplyDailyCaps,
		StepApplyProportionateDeduction,
		StepApplySubLimits,
		StepCapAtCoverLeft,
		StepTakeCoPayment,
		StepTakeBillDiscount,
	}
}

// Targets returns what a step's rules may apply to. A step's rules cover each
// target exactly once, so the engine can find the rule for every case it
// handles.
func Targets(s StepName) []string {
	switch s {
	case StepCheckEligibility:
		// The condition's waiting-period group; the 30-day wait covers any
		// illness.
		return []string{"any-illness", "listed-24-months", "listed-36-months", "pre-existing"}
	case StepDropListedItems:
		return []string{"list-i", "list-ii", "list-iii", "list-iv", "list-v"}
	case StepSetAsideUnmodelledLines:
		return []string{"package-rate", "outside-stay"}
	case StepApplyDailyCaps, StepApplyProportionateDeduction:
		return []string{"room", "icu"}
	case StepApplySubLimits:
		return []string{"cataract", "modern-treatment", "road-ambulance"}
	case StepCapAtCoverLeft, StepTakeCoPayment, StepTakeBillDiscount:
		return []string{"claim"}
	}
	return nil
}

// Category is a line's billing category. Every pack puts each category under
// exactly one treatment: a daily-cap bucket, scaled, or exempt.
type Category string

// The line categories.
const (
	CategoryRoomRent             Category = "room-rent"
	CategoryNursing              Category = "nursing"
	CategoryPatientDiet          Category = "patient-diet"
	CategoryICUCharges           Category = "icu-charges"
	CategoryProfessionalFees     Category = "professional-fees"
	CategoryOperationTheatre     Category = "operation-theatre"
	CategoryOtherHospitalCharges Category = "other-hospital-charges"
	CategoryPharmacyConsumables  Category = "pharmacy-consumables"
	CategoryImplantsDevices      Category = "implants-devices"
	CategoryDiagnostics          Category = "diagnostics"
	CategoryRoadAmbulance        Category = "road-ambulance"
)

// Categories returns every line category.
func Categories() []Category {
	return []Category{
		CategoryRoomRent,
		CategoryNursing,
		CategoryPatientDiet,
		CategoryICUCharges,
		CategoryProfessionalFees,
		CategoryOperationTheatre,
		CategoryOtherHospitalCharges,
		CategoryPharmacyConsumables,
		CategoryImplantsDevices,
		CategoryDiagnostics,
		CategoryRoadAmbulance,
	}
}

// ListEffect is what an Annexure A list does to a line it matches. Every
// effect pays the line nothing; the effect records why.
type ListEffect string

// The list effects.
const (
	EffectNotPayable       ListEffect = "not-payable"
	EffectRoomCharges      ListEffect = "room-charges"
	EffectProcedureCharges ListEffect = "procedure-charges"
	EffectTreatmentCosts   ListEffect = "treatment-costs"
)

func listEffects() []ListEffect {
	return []ListEffect{EffectNotPayable, EffectRoomCharges, EffectProcedureCharges, EffectTreatmentCosts}
}

// Period is what a sub-limit is counted over.
type Period string

// The sub-limit periods.
const (
	PerEyePerPolicyYear Period = "eye-per-policy-year"
	PerPolicyYear       Period = "policy-year"
	PerHospitalisation  Period = "hospitalisation"
)

func periods() []Period {
	return []Period{PerEyePerPolicyYear, PerPolicyYear, PerHospitalisation}
}

// RatioBasis is what proportionate deduction divides by. The only basis the
// engine supports is the bucket's daily total, the amount the cap measures.
type RatioBasis string

// RatioBucketDailyTotal divides the capped bucket total by the billed bucket
// total.
const RatioBucketDailyTotal RatioBasis = "bucket-daily-total"

func ratioBases() []RatioBasis { return []RatioBasis{RatioBucketDailyTotal} }
