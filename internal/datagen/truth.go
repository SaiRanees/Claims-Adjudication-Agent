package datagen

// Version is the generator's version. Truth carries it, and any change that
// could move a packet's bytes or its truth needs a new one, as a rule pack
// change needs a new version.
const Version = "1"

// Truth is what a rendered packet holds, for scoring extraction against it.
// Every printed string is exactly what the page shows, beside the value it
// means. Page numbers count from 1 within a file.
type Truth struct {
	GeneratorVersion string      `json:"generator_version"`
	Scenario         string      `json:"scenario"`
	GoldenCase       string      `json:"golden_case,omitempty"`
	Files            []TruthFile `json:"files"`
	// Documents lists the types of the documents included, sorted, for the
	// packet checklist.
	Documents []DocumentType   `json:"documents"`
	Bills     []TruthBill      `json:"bills"`
	Receipts  []TruthReceipt   `json:"receipts"`
	Claim     TruthClaim       `json:"claim"`
	Engine    TruthEngineInput `json:"engine"`
}

// TruthFile is one file: each page's type and the parts it splits into.
type TruthFile struct {
	Name   string      `json:"name"`
	SHA256 string      `json:"sha256"`
	Pages  []TruthPage `json:"pages"`
	Parts  []TruthPart `json:"parts"`
}

// TruthPage is one page of a file.
type TruthPage struct {
	Page     int          `json:"page"`
	Type     DocumentType `json:"type"`
	Document string       `json:"document"`
}

// TruthPart is a logical document: a type and a page range within a file.
type TruthPart struct {
	Document  string       `json:"document"`
	Type      DocumentType `json:"type"`
	FirstPage int          `json:"first_page"`
	LastPage  int          `json:"last_page"`
}

// TruthBill is one bill as printed.
type TruthBill struct {
	Document  string       `json:"document"`
	Type      DocumentType `json:"type"`
	Layout    Layout       `json:"layout,omitempty"`
	File      string       `json:"file"`
	FirstPage int          `json:"first_page"`
	LastPage  int          `json:"last_page"`
	// Number, Date and Patient are printed on every page of the bill.
	Number  string      `json:"number"`
	Date    PrintedDate `json:"date"`
	Patient string      `json:"patient"`
	Lines   []TruthLine `json:"lines"`
	Totals  TruthTotals `json:"totals"`
}

// TruthLine is one line of a bill as printed.
type TruthLine struct {
	// Line is the facts line it prints.
	Line string `json:"line"`
	Page int    `json:"page"`
	// ServiceDate is nil for an undated line, whose date cell is blank.
	ServiceDate *PrintedDate   `json:"service_date"`
	Description string         `json:"description"`
	Quantity    PrintedInteger `json:"quantity"`
	UnitPrice   PrintedAmount  `json:"unit_price"`
	Amount      PrintedAmount  `json:"amount"`
}

// TruthTotals are a bill's totals, printed on its last page. Gross is the
// lines' sum and Net is Gross less Discount.
type TruthTotals struct {
	Page  int           `json:"page"`
	Gross PrintedAmount `json:"gross"`
	// Discount is nil when the bill prints none.
	Discount *PrintedAmount `json:"discount"`
	Net      PrintedAmount  `json:"net"`
}

// TruthReceipt is one payment receipt as printed.
type TruthReceipt struct {
	Document string        `json:"document"`
	File     string        `json:"file"`
	Page     int           `json:"page"`
	Number   string        `json:"number"`
	Date     PrintedDate   `json:"date"`
	Amount   PrintedAmount `json:"amount"`
	// Against lists the bills it pays, by document ID.
	Against []string `json:"against"`
}

// PrintedAmount is an amount as printed and in paise.
type PrintedAmount struct {
	Printed string `json:"printed"`
	Paise   int64  `json:"paise"`
}

// PrintedDate is a date as printed and as YYYY-MM-DD.
type PrintedDate struct {
	Printed string `json:"printed"`
	Date    string `json:"date"`
}

// PrintedInteger is a whole number as printed and as a value.
type PrintedInteger struct {
	Printed string `json:"printed"`
	Value   int64  `json:"value"`
}

// TruthClaim is the claim-level facts week 4's discharge-summary job reads.
type TruthClaim struct {
	Admission      string        `json:"admission"`
	Discharge      string        `json:"discharge"`
	Segments       []SegmentSpec `json:"segments"`
	Diagnosis      string        `json:"diagnosis"`
	Procedure      string        `json:"procedure"`
	TreatingDoctor string        `json:"treating_doctor"`
	Accident       bool          `json:"accident"`
	Onset          string        `json:"onset"`
	ListedGroup    string        `json:"listed_group"`
	PreExisting    bool          `json:"pre_existing"`
}

// TruthEngineInput is the spec's policy, cover and facts, unchanged, for M3
// and the engine test.
type TruthEngineInput struct {
	Policy PolicySpec `json:"policy"`
	Cover  CoverSpec  `json:"cover"`
	Facts  FactsSpec  `json:"facts"`
}
