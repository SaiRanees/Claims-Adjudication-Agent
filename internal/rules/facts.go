package rules

import "github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"

// Facts are what a claim's documents establish: the stay, the bill and the
// condition. Amounts are paise. The agent gathers them and checks them
// against the documents (M3); the engine only computes.
type Facts struct {
	Admission Date
	Discharge Date
	// Segments split the stay by room kind and daily rate. They tile it from
	// admission to discharge in date order, so each date falls in exactly
	// one; a transfer day belongs to the new room.
	Segments []Segment
	// DifferentialBilling says whether the hospital bills by room category
	// (C.I.1 Note 3).
	DifferentialBilling Answer
	// BillDiscount is a discount on the whole bill, at least zero.
	BillDiscount money.Paise
	Lines        []Line
	// Condition is read from week 2, session 5.
	Condition Condition
}

// Answer is a fact that may be unknown.
type Answer string

// The answers.
const (
	AnswerYes     Answer = "yes"
	AnswerNo      Answer = "no"
	AnswerUnknown Answer = "unknown"
)

func answers() []Answer { return []Answer{AnswerYes, AnswerNo, AnswerUnknown} }

// Segment is a run of days in one kind of room at one daily rate. From and To
// are inclusive.
type Segment struct {
	From Date
	To   Date
	Kind SegmentKind
}

// SegmentKind is which daily-cap bucket a segment's days belong to. The
// values are the daily-cap rules' targets.
type SegmentKind string

// The segment kinds.
const (
	SegmentRoom SegmentKind = "room"
	SegmentICU  SegmentKind = "icu"
)

func segmentKinds() []SegmentKind { return []SegmentKind{SegmentRoom, SegmentICU} }

// Line is one line of the hospital bill.
type Line struct {
	ID       string
	Category Category
	// ServiceDate is the zero Date for an undated line. A bucket line's date
	// is its first billed day, and picks its segment.
	ServiceDate Date
	// Quantity is the units billed; for a bucket line, the days billed.
	Quantity  int64
	UnitPrice money.Paise
	// Amount is Quantity × UnitPrice.
	Amount money.Paise
	// ItemList is the Annexure A list the line matched, such as "list-ii",
	// or empty. The values are the drop-listed-items rules' targets.
	ItemList string
	// PackageRate marks a line billed at a package rate, which the engine
	// doesn't model.
	PackageRate bool
	// Treatment ties the line to a treatment with a sub-limit. Road
	// ambulance lines are tied by their category.
	Treatment Treatment
}

// Treatment is the treatment a line is tied to.
type Treatment struct {
	Kind TreatmentKind
	// Eye is set for a cataract, and only then.
	Eye Eye
}

// TreatmentKind is a treatment with a sub-limit. The values are the
// sub-limit rules' targets.
type TreatmentKind string

// The treatment kinds. TreatmentNone is the zero value.
const (
	TreatmentNone            TreatmentKind = ""
	TreatmentCataract        TreatmentKind = "cataract"
	TreatmentModernTreatment TreatmentKind = "modern-treatment"
)

func treatmentKinds() []TreatmentKind {
	return []TreatmentKind{TreatmentNone, TreatmentCataract, TreatmentModernTreatment}
}

// Eye is the eye a cataract line treats.
type Eye string

// The eyes. EyeNone is the zero value, for every line that isn't a cataract.
const (
	EyeNone  Eye = ""
	EyeLeft  Eye = "left"
	EyeRight Eye = "right"
)

// Condition is what the waiting periods test (week 2, session 5). Deciding
// it from the documents is the agent's job in M3.
type Condition struct {
	// ListedGroup is the condition's Excl02 group, "listed-24-months" or
	// "listed-36-months", or empty when it isn't listed.
	ListedGroup string
	PreExisting bool
	Accident    bool
	// Onset is the zero Date when unknown.
	Onset Date
}

func listedGroups() []string { return []string{"", "listed-24-months", "listed-36-months"} }

// Cover is the policy year's cover and what earlier claims used of it, read
// from the ledger. Its terms match Ledger, so the next claim reads back what
// this one wrote.
type Cover struct {
	// Tranches are the sum insured, split by when each part's continuous
	// cover began, portability included. An increase is its own tranche,
	// because the waiting periods restart for it.
	Tranches []Tranche
	// CumulativeBonus is the bonus granted for the policy year.
	CumulativeBonus money.Paise
	// PaidThisYear is what earlier claims paid this policy year.
	PaidThisYear money.Paise
	// CataractUsed is each eye's cataract sub-limit used this policy year.
	CataractUsed EyeAmounts
	// ModernTreatmentUsed is the C.I.6 sub-limit used this policy year.
	ModernTreatmentUsed money.Paise
}

// Tranche is part of the sum insured, with the date its continuous cover
// began.
type Tranche struct {
	SumInsured      money.Paise
	ContinuousSince Date
}

// EyeAmounts is an amount for each eye.
type EyeAmounts struct {
	Left  money.Paise
	Right money.Paise
}
