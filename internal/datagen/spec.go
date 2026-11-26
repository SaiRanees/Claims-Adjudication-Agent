package datagen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// Spec is a scenario: everything a packet shows. Policy, Cover and Facts
// have the golden-case format's shape and are the engine's input; the
// generator never changes them. The rest is how the packet presents them.
type Spec struct {
	// ID is the spec file's name without .yaml. It isn't in the file.
	ID string `yaml:"-" json:"-"`
	// GoldenCase names the golden case a golden spec renders, whose policy,
	// cover and facts it must hold unchanged. Seeded specs leave it empty.
	GoldenCase string       `yaml:"golden_case,omitempty" json:"golden_case,omitempty"`
	Policy     PolicySpec   `yaml:"policy" json:"policy"`
	Cover      CoverSpec    `yaml:"cover" json:"cover"`
	Facts      FactsSpec    `yaml:"facts" json:"facts"`
	Insured    Insured      `yaml:"insured" json:"-"`
	Hospital   Hospital     `yaml:"hospital" json:"-"`
	Clinical   Clinical     `yaml:"clinical" json:"-"`
	ClaimForm  ClaimForm    `yaml:"claim_form" json:"-"`
	Documents  []Document   `yaml:"documents" json:"-"`
	Files      []FileSpec   `yaml:"files" json:"-"`
	Lines      []LineFormat `yaml:"lines" json:"-"`
}

// PolicySpec is the golden format's policy block.
type PolicySpec struct {
	WordingUIN  string `yaml:"wording_uin" json:"wording_uin"`
	PeriodStart string `yaml:"period_start" json:"period_start"`
	PeriodEnd   string `yaml:"period_end" json:"period_end"`
}

// CoverSpec is the golden format's cover block. Money is paise.
type CoverSpec struct {
	Tranches            []TrancheSpec `yaml:"tranches" json:"tranches"`
	CumulativeBonus     int64         `yaml:"cumulative_bonus_paise,omitempty" json:"cumulative_bonus_paise"`
	PaidThisYear        int64         `yaml:"paid_this_year_paise,omitempty" json:"paid_this_year_paise"`
	CataractUsed        EyeSpec       `yaml:"cataract_used_paise,omitempty" json:"cataract_used_paise"`
	ModernTreatmentUsed int64         `yaml:"modern_treatment_used_paise,omitempty" json:"modern_treatment_used_paise"`
}

// TrancheSpec is one tranche of the sum insured.
type TrancheSpec struct {
	SumInsured      int64  `yaml:"sum_insured_paise" json:"sum_insured_paise"`
	ContinuousSince string `yaml:"continuous_since" json:"continuous_since"`
}

// EyeSpec is an amount for each eye.
type EyeSpec struct {
	Left  int64 `yaml:"left,omitempty" json:"left"`
	Right int64 `yaml:"right,omitempty" json:"right"`
}

// FactsSpec is the golden format's facts block.
type FactsSpec struct {
	Admission           string        `yaml:"admission" json:"admission"`
	Discharge           string        `yaml:"discharge" json:"discharge"`
	Segments            []SegmentSpec `yaml:"segments" json:"segments"`
	DifferentialBilling string        `yaml:"differential_billing" json:"differential_billing"`
	BillDiscount        int64         `yaml:"bill_discount_paise,omitempty" json:"bill_discount_paise"`
	Condition           ConditionSpec `yaml:"condition,omitempty" json:"condition"`
	Lines               []LineSpec    `yaml:"lines" json:"lines"`
}

// SegmentSpec is one segment of the stay.
type SegmentSpec struct {
	From string `yaml:"from" json:"from"`
	To   string `yaml:"to" json:"to"`
	Kind string `yaml:"kind" json:"kind"`
}

// ConditionSpec is what the waiting periods test.
type ConditionSpec struct {
	ListedGroup string `yaml:"listed_group,omitempty" json:"listed_group"`
	PreExisting bool   `yaml:"pre_existing,omitempty" json:"pre_existing"`
	Accident    bool   `yaml:"accident,omitempty" json:"accident"`
	Onset       string `yaml:"onset,omitempty" json:"onset"`
}

// LineSpec is one bill line's facts.
type LineSpec struct {
	ID          string        `yaml:"id" json:"id"`
	Category    string        `yaml:"category" json:"category"`
	ServiceDate string        `yaml:"service_date,omitempty" json:"service_date"`
	Quantity    int64         `yaml:"quantity" json:"quantity"`
	UnitPrice   int64         `yaml:"unit_price_paise" json:"unit_price_paise"`
	Amount      int64         `yaml:"amount_paise" json:"amount_paise"`
	ItemList    string        `yaml:"item_list,omitempty" json:"item_list"`
	PackageRate bool          `yaml:"package_rate,omitempty" json:"package_rate"`
	Treatment   TreatmentSpec `yaml:"treatment,omitempty" json:"treatment"`
}

// TreatmentSpec ties a line to a treatment with a sub-limit.
type TreatmentSpec struct {
	Kind string `yaml:"kind,omitempty" json:"kind"`
	Eye  string `yaml:"eye,omitempty" json:"eye"`
}

// Insured is the policyholder as the documents print them. Invented.
type Insured struct {
	Name         string `yaml:"name"`
	Gender       string `yaml:"gender"`
	Age          int    `yaml:"age"`
	PolicyNumber string `yaml:"policy_number"`
	MemberID     string `yaml:"member_id"`
	// DateOfBirth is printed on the photo ID and the claim form. A spec
	// with a photo ID needs it, and Age must be the age it gives at the
	// admission.
	DateOfBirth string `yaml:"date_of_birth,omitempty"`
}

// Hospital is the treating hospital. Invented.
type Hospital struct {
	Name         string `yaml:"name"`
	Address      string `yaml:"address"`
	City         string `yaml:"city"`
	Registration string `yaml:"registration"`
}

// Clinical is the condition as the documents describe it.
type Clinical struct {
	Diagnosis      string `yaml:"diagnosis"`
	Procedure      string `yaml:"procedure,omitempty"`
	TreatingDoctor string `yaml:"treating_doctor"`
	// History, Course and Advice are the discharge summary's paragraphs. A
	// spec with a discharge summary needs Course and Advice.
	History string `yaml:"history,omitempty"`
	Course  string `yaml:"course,omitempty"`
	Advice  string `yaml:"advice,omitempty"`
	// Surgery is what the OT notes record. A spec with OT notes needs it
	// and a procedure.
	Surgery *Surgery `yaml:"surgery,omitempty"`
}

// Surgery is one operation, as the OT notes record it.
type Surgery struct {
	Date         string `yaml:"date"`
	Surgeon      string `yaml:"surgeon"`
	Anaesthetist string `yaml:"anaesthetist"`
	Anaesthesia  string `yaml:"anaesthesia"`
	Findings     string `yaml:"findings"`
}

// ClaimForm is the claim form's values.
type ClaimForm struct {
	AdmissionType string `yaml:"admission_type"`
	SignedOn      string `yaml:"signed_on"`
	ClaimedAmount int64  `yaml:"claimed_amount_paise"`
}

// DocumentType is a logical document's type. The values follow the packet
// checklist.
type DocumentType string

// The document types the generator draws.
const (
	DocHospitalBill     DocumentType = "hospital-bill"
	DocPharmacyBill     DocumentType = "pharmacy-bill"
	DocPaymentReceipt   DocumentType = "payment-receipt"
	DocClaimForm        DocumentType = "claim-form"
	DocPhotoID          DocumentType = "photo-id"
	DocDischargeSummary DocumentType = "discharge-summary"
	DocOTNotes          DocumentType = "ot-notes"
	DocImplantInvoice   DocumentType = "implant-invoice"
)

// DocumentTypes lists the types the generator draws.
func DocumentTypes() []DocumentType {
	return []DocumentType{
		DocHospitalBill, DocPharmacyBill, DocPaymentReceipt, DocClaimForm,
		DocPhotoID, DocDischargeSummary, DocOTNotes, DocImplantInvoice,
	}
}

// Document is one logical document in the packet.
type Document struct {
	ID     string       `yaml:"id"`
	Type   DocumentType `yaml:"type"`
	Number string       `yaml:"number"`
	Date   string       `yaml:"date"`
	// Layout is a hospital bill's layout.
	Layout Layout `yaml:"layout,omitempty"`
	// LinesPerPage is how many line rows a bill's page holds.
	LinesPerPage int `yaml:"lines_per_page,omitempty"`
	// Discount is a bill's discount, printed in its totals. The bills'
	// discounts add up to the facts' bill discount.
	Discount int64 `yaml:"discount_paise,omitempty"`
	// Against lists the bills a receipt pays.
	Against []string `yaml:"against,omitempty"`
	// Mode is how a receipt was paid.
	Mode string `yaml:"mode,omitempty"`
	// Vendor issued an implant invoice.
	Vendor *Vendor `yaml:"vendor,omitempty"`
	// Implants are an implant invoice's items: each an implants-devices
	// line of a hospital bill, invoiced at the bill's quantity and price.
	Implants []Implant `yaml:"implants,omitempty"`
}

// Vendor is an implant's supplier. Invented.
type Vendor struct {
	Name         string `yaml:"name"`
	Address      string `yaml:"address"`
	Registration string `yaml:"registration"`
}

// Implant is one item of an implant invoice. The OT notes print it too.
type Implant struct {
	Line   string `yaml:"line"`
	Serial string `yaml:"serial"`
}

// IsBill reports whether the document lists lines.
func (d Document) IsBill() bool { return d.Type == DocHospitalBill || d.Type == DocPharmacyBill }

// Layout is a hospital bill's layout.
type Layout string

// The hospital-bill layouts.
const (
	LayoutLedger   Layout = "ledger"
	LayoutColumnar Layout = "columnar"
	LayoutCompact  Layout = "compact"
)

// Layouts lists the hospital-bill layouts.
func Layouts() []Layout { return []Layout{LayoutLedger, LayoutColumnar, LayoutCompact} }

// FileSpec is one uploaded file: its documents, in page order.
type FileSpec struct {
	Name      string   `yaml:"name"`
	Documents []string `yaml:"documents"`
}

// LineFormat is how one facts line is printed.
type LineFormat struct {
	Line        string `yaml:"line"`
	Bill        string `yaml:"bill"`
	Description string `yaml:"description"`
	// Annexure names the Annexure A item a listed line matches, such as
	// "list-ii/27". Its description is the item's name in the wording,
	// unless MatchedBy says the match came from something else.
	Annexure string `yaml:"annexure,omitempty"`
	// MatchedBy is empty when the line prints the item's name. Otherwise
	// the line prints its own description and matched the item through the
	// item's explanation in the wording ("explanation", as case 21's
	// biomedical waste matches AHD), or through the stay's purpose ("stay",
	// as case 31's evaluation stay matches List IV item 2).
	MatchedBy MatchedBy `yaml:"matched_by,omitempty"`
}

// MatchedBy is how a line printed under its own description matched an
// Annexure A item.
type MatchedBy string

// The ways a line can match an item other than by its name.
const (
	MatchedByName        MatchedBy = ""
	MatchedByExplanation MatchedBy = "explanation"
	MatchedByStay        MatchedBy = "stay"
)

// ParseSpec decodes one spec file strictly, as the rule pack is decoded:
// unknown fields and a second document are errors. Then it validates it.
func ParseSpec(id string, src []byte) (Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("spec %s: %w", id, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Spec{}, fmt.Errorf("spec %s: more than one document", id)
	}
	s.ID = id
	if err := s.Validate(); err != nil {
		return Spec{}, fmt.Errorf("spec %s: %w", id, err)
	}
	return s, nil
}

// Engine converts the policy, cover and facts into the engine's input.
func (s Spec) Engine() (rules.PolicyPeriod, rules.Cover, rules.Facts, error) {
	return EngineInput(s.Policy, s.Cover, s.Facts)
}

// EngineInput converts golden-format blocks into the engine's input. An
// empty date is the zero Date.
func EngineInput(p PolicySpec, c CoverSpec, f FactsSpec) (rules.PolicyPeriod, rules.Cover, rules.Facts, error) {
	var d dateParser
	period := rules.PolicyPeriod{
		WordingUIN: p.WordingUIN,
		Start:      d.parse("policy.period_start", p.PeriodStart),
		End:        d.parse("policy.period_end", p.PeriodEnd),
	}
	var cover rules.Cover
	for i, t := range c.Tranches {
		cover.Tranches = append(cover.Tranches, rules.Tranche{
			SumInsured:      money.Paise(t.SumInsured),
			ContinuousSince: d.parse(fmt.Sprintf("cover.tranches[%d].continuous_since", i), t.ContinuousSince),
		})
	}
	cover.CumulativeBonus = money.Paise(c.CumulativeBonus)
	cover.PaidThisYear = money.Paise(c.PaidThisYear)
	cover.CataractUsed = rules.EyeAmounts{Left: money.Paise(c.CataractUsed.Left), Right: money.Paise(c.CataractUsed.Right)}
	cover.ModernTreatmentUsed = money.Paise(c.ModernTreatmentUsed)

	var facts rules.Facts
	facts.Admission = d.parse("facts.admission", f.Admission)
	facts.Discharge = d.parse("facts.discharge", f.Discharge)
	for i, seg := range f.Segments {
		facts.Segments = append(facts.Segments, rules.Segment{
			From: d.parse(fmt.Sprintf("facts.segments[%d].from", i), seg.From),
			To:   d.parse(fmt.Sprintf("facts.segments[%d].to", i), seg.To),
			Kind: rules.SegmentKind(seg.Kind),
		})
	}
	facts.DifferentialBilling = rules.Answer(f.DifferentialBilling)
	facts.BillDiscount = money.Paise(f.BillDiscount)
	facts.Condition = rules.Condition{
		ListedGroup: f.Condition.ListedGroup,
		PreExisting: f.Condition.PreExisting,
		Accident:    f.Condition.Accident,
		Onset:       d.parse("facts.condition.onset", f.Condition.Onset),
	}
	for i, l := range f.Lines {
		facts.Lines = append(facts.Lines, rules.Line{
			ID:          l.ID,
			Category:    rules.Category(l.Category),
			ServiceDate: d.parse(fmt.Sprintf("facts.lines[%d].service_date", i), l.ServiceDate),
			Quantity:    l.Quantity,
			UnitPrice:   money.Paise(l.UnitPrice),
			Amount:      money.Paise(l.Amount),
			ItemList:    l.ItemList,
			PackageRate: l.PackageRate,
			Treatment:   rules.Treatment{Kind: rules.TreatmentKind(l.Treatment.Kind), Eye: rules.Eye(l.Treatment.Eye)},
		})
	}
	return period, cover, facts, d.err
}

type dateParser struct{ err error }

func (d *dateParser) parse(field, s string) rules.Date {
	if s == "" || d.err != nil {
		return rules.Date{}
	}
	v, err := rules.ParseDate(s)
	if err != nil {
		d.err = fmt.Errorf("%s: %w", field, err)
	}
	return v
}

// The shapes of names the generator prints into file names and IDs.
var (
	idPattern       = regexp.MustCompile(`^[A-Z][A-Z0-9]{0,7}$`)
	fileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}\.pdf$`)
)

// Validate checks what the decoder can't: the engine's input converts, and
// every presentation field is set, refers to something that exists and
// agrees with the facts.
func (s Spec) Validate() error {
	_, _, facts, err := s.Engine()
	if err != nil {
		return err
	}
	if s.Facts.Admission == "" || s.Facts.Discharge == "" {
		return errors.New("facts need an admission and a discharge date")
	}
	if err := s.validatePeople(); err != nil {
		return err
	}
	docs, err := s.validateDocuments()
	if err != nil {
		return err
	}
	if err := s.validateFiles(docs); err != nil {
		return err
	}
	if err := s.validateLines(facts, docs); err != nil {
		return err
	}
	return s.validateRecords(facts)
}

func (s Spec) validatePeople() error {
	required := map[string]string{
		"insured.name": s.Insured.Name, "insured.gender": s.Insured.Gender,
		"insured.policy_number": s.Insured.PolicyNumber, "insured.member_id": s.Insured.MemberID,
		"hospital.name": s.Hospital.Name, "hospital.address": s.Hospital.Address,
		"hospital.city": s.Hospital.City, "hospital.registration": s.Hospital.Registration,
		"clinical.diagnosis": s.Clinical.Diagnosis, "clinical.treating_doctor": s.Clinical.TreatingDoctor,
		"claim_form.admission_type": s.ClaimForm.AdmissionType, "claim_form.signed_on": s.ClaimForm.SignedOn,
	}
	for _, k := range sortedKeys(required) {
		if required[k] == "" {
			return fmt.Errorf("%s is empty", k)
		}
	}
	if s.Insured.Age < 1 || s.Insured.Age > 120 {
		return fmt.Errorf("insured.age %d isn't 1 to 120", s.Insured.Age)
	}
	if _, err := rules.ParseDate(s.ClaimForm.SignedOn); err != nil {
		return fmt.Errorf("claim_form.signed_on: %w", err)
	}
	if s.ClaimForm.ClaimedAmount <= 0 {
		return errors.New("claim_form.claimed_amount_paise must be positive")
	}
	return nil
}

func (s Spec) validateDocuments() (map[string]Document, error) {
	docs := map[string]Document{}
	var discounts int64
	for i, d := range s.Documents {
		where := fmt.Sprintf("documents[%d]", i)
		if !idPattern.MatchString(d.ID) {
			return nil, fmt.Errorf("%s: id %q isn't 1 to 8 capitals and digits", where, d.ID)
		}
		if _, dup := docs[d.ID]; dup {
			return nil, fmt.Errorf("%s: id %s repeats", where, d.ID)
		}
		if d.Number == "" {
			return nil, fmt.Errorf("%s: no number", where)
		}
		if _, err := rules.ParseDate(d.Date); err != nil {
			return nil, fmt.Errorf("%s: date: %w", where, err)
		}
		switch d.Type {
		case DocHospitalBill:
			if !slices.Contains(Layouts(), d.Layout) {
				return nil, fmt.Errorf("%s: layout %q isn't one of %v", where, d.Layout, Layouts())
			}
		case DocPharmacyBill:
			if d.Layout != "" {
				return nil, fmt.Errorf("%s: a pharmacy bill has one layout, so no layout field", where)
			}
		case DocPaymentReceipt:
			if len(d.Against) == 0 || d.Mode == "" {
				return nil, fmt.Errorf("%s: a receipt needs against and mode", where)
			}
		case DocClaimForm, DocPhotoID, DocDischargeSummary, DocOTNotes, DocImplantInvoice:
			if d.Layout != "" || len(d.Against) > 0 || d.Mode != "" {
				return nil, fmt.Errorf("%s: a %s has no layout, against or mode", where, d.Type)
			}
		default:
			return nil, fmt.Errorf("%s: type %q isn't one of %v", where, d.Type, DocumentTypes())
		}
		if d.Type != DocImplantInvoice && (d.Vendor != nil || len(d.Implants) > 0) {
			return nil, fmt.Errorf("%s: only an implant invoice has a vendor or implants", where)
		}
		if d.IsBill() {
			if d.LinesPerPage < 1 || d.LinesPerPage > maxLinesPerPage {
				return nil, fmt.Errorf("%s: lines_per_page %d isn't 1 to %d", where, d.LinesPerPage, maxLinesPerPage)
			}
			if d.Discount < 0 {
				return nil, fmt.Errorf("%s: negative discount", where)
			}
			discounts += d.Discount
		} else if d.LinesPerPage != 0 || d.Discount != 0 {
			return nil, fmt.Errorf("%s: only a bill has lines_per_page or a discount", where)
		}
		docs[d.ID] = d
	}
	count := map[DocumentType]int{}
	for _, d := range s.Documents {
		count[d.Type]++
	}
	for _, t := range singleDocuments {
		if count[t] > 1 {
			return nil, fmt.Errorf("a packet holds at most one %s, not %d", t, count[t])
		}
	}
	if discounts != s.Facts.BillDiscount {
		return nil, fmt.Errorf("the bills' discounts add up to %d paise, but facts.bill_discount_paise is %d", discounts, s.Facts.BillDiscount)
	}
	for _, d := range s.Documents {
		for _, b := range d.Against {
			if !docs[b].IsBill() {
				return nil, fmt.Errorf("receipt %s pays %q, which isn't a bill", d.ID, b)
			}
		}
	}
	return docs, nil
}

func (s Spec) validateFiles(docs map[string]Document) error {
	placed := map[string]bool{}
	names := map[string]bool{}
	for i, f := range s.Files {
		if !fileNamePattern.MatchString(f.Name) || names[f.Name] {
			return fmt.Errorf("files[%d]: name %q is repeated or isn't lower-case letters, digits and hyphens ending .pdf", i, f.Name)
		}
		names[f.Name] = true
		if len(f.Documents) == 0 {
			return fmt.Errorf("files[%d]: no documents", i)
		}
		for _, id := range f.Documents {
			if _, ok := docs[id]; !ok || placed[id] {
				return fmt.Errorf("files[%d]: document %q is unknown or already in a file", i, id)
			}
			placed[id] = true
		}
	}
	for _, d := range s.Documents {
		if !placed[d.ID] {
			return fmt.Errorf("document %s is in no file", d.ID)
		}
	}
	return nil
}

func (s Spec) validateLines(facts rules.Facts, docs map[string]Document) error {
	formats := map[string]LineFormat{}
	for i, lf := range s.Lines {
		if _, dup := formats[lf.Line]; dup {
			return fmt.Errorf("lines[%d]: line %s repeats", i, lf.Line)
		}
		formats[lf.Line] = lf
	}
	billed := map[string]int{}
	for _, l := range facts.Lines {
		lf, ok := formats[l.ID]
		if !ok {
			return fmt.Errorf("facts line %s has no entry in lines", l.ID)
		}
		delete(formats, l.ID)
		bill, ok := docs[lf.Bill]
		if !ok || !bill.IsBill() {
			return fmt.Errorf("line %s: bill %q isn't a bill", l.ID, lf.Bill)
		}
		billed[lf.Bill]++
		if bill.Type == DocPharmacyBill && l.Category != rules.CategoryPharmacyConsumables {
			return fmt.Errorf("line %s: a pharmacy bill lists only pharmacy-consumables, not %s", l.ID, l.Category)
		}
		if l.Quantity < 1 {
			return fmt.Errorf("line %s: quantity %d isn't a whole number of at least 1", l.ID, l.Quantity)
		}
		if lf.Description == "" {
			return fmt.Errorf("line %s: no description", l.ID)
		}
		if err := checkAnnexure(l, lf); err != nil {
			return err
		}
	}
	if left := sortedKeys(formats); len(left) > 0 {
		return fmt.Errorf("lines: %s isn't a facts line", left[0])
	}
	for _, d := range s.Documents {
		if d.IsBill() && billed[d.ID] == 0 {
			return fmt.Errorf("bill %s has no lines", d.ID)
		}
	}
	return nil
}

// The shapes of the photo ID's number and an implant's serial. Both start
// SYN-, so neither copies the format of a real ID or serial.
var (
	photoIDPattern = regexp.MustCompile(`^SYN-ID-[A-Z0-9]{4}-[0-9]{4}$`)
	serialPattern  = regexp.MustCompile(`^SYN-SN-[A-Z0-9]{6}$`)
)

// singleDocuments are the types a packet holds at most one of.
var singleDocuments = []DocumentType{DocClaimForm, DocPhotoID, DocDischargeSummary, DocOTNotes}

// validateRecords checks the documents that aren't bills or receipts: each
// has what it prints, and an implant invoice's items are hospital-bill
// implant lines, each invoiced once.
func (s Spec) validateRecords(facts rules.Facts) error {
	count := map[DocumentType]int{}
	for _, d := range s.Documents {
		count[d.Type]++
	}
	if err := s.validateBirth(facts.Admission, count[DocPhotoID] > 0); err != nil {
		return err
	}
	if err := s.validateSurgery(facts, count[DocOTNotes] > 0); err != nil {
		return err
	}
	if count[DocDischargeSummary] > 0 && (s.Clinical.Course == "" || s.Clinical.Advice == "") {
		return errors.New("a discharge summary needs clinical.course and clinical.advice")
	}
	lines := map[string]rules.Line{}
	for _, l := range facts.Lines {
		lines[l.ID] = l
	}
	bills := map[string]DocumentType{}
	for _, d := range s.Documents {
		bills[d.ID] = d.Type
	}
	onBill := map[string]string{}
	for _, lf := range s.Lines {
		onBill[lf.Line] = lf.Bill
	}
	invoiced := map[string]bool{}
	for _, d := range s.Documents {
		switch d.Type {
		case DocClaimForm:
			if d.Date != s.ClaimForm.SignedOn {
				return fmt.Errorf("claim form %s is dated %s, but claim_form.signed_on is %s", d.ID, d.Date, s.ClaimForm.SignedOn)
			}
		case DocPhotoID:
			if !photoIDPattern.MatchString(d.Number) {
				return fmt.Errorf("photo ID %s: number %q isn't SYN-ID-XXXX-9999", d.ID, d.Number)
			}
		case DocImplantInvoice:
			v := d.Vendor
			if v == nil || v.Name == "" || v.Address == "" || v.Registration == "" || len(d.Implants) == 0 {
				return fmt.Errorf("implant invoice %s needs a vendor's name, address and registration, and implants", d.ID)
			}
			for _, im := range d.Implants {
				l, ok := lines[im.Line]
				switch {
				case !ok || l.Category != rules.CategoryImplantsDevices:
					return fmt.Errorf("implant invoice %s: line %q isn't an implants-devices line", d.ID, im.Line)
				case bills[onBill[im.Line]] != DocHospitalBill:
					return fmt.Errorf("implant invoice %s: line %s isn't on a hospital bill", d.ID, im.Line)
				case invoiced[im.Line]:
					return fmt.Errorf("implant invoice %s: line %s is invoiced twice", d.ID, im.Line)
				case !serialPattern.MatchString(im.Serial):
					return fmt.Errorf("implant invoice %s: serial %q isn't SYN-SN-XXXXXX", d.ID, im.Serial)
				}
				invoiced[im.Line] = true
			}
		case DocHospitalBill, DocPharmacyBill, DocPaymentReceipt, DocDischargeSummary, DocOTNotes:
		}
	}
	return nil
}

// validateBirth checks the date of birth against the age, which the
// documents print as the age at the admission.
func (s Spec) validateBirth(admission rules.Date, needed bool) error {
	if s.Insured.DateOfBirth == "" {
		if needed {
			return errors.New("a photo ID needs insured.date_of_birth")
		}
		return nil
	}
	dob, err := rules.ParseDate(s.Insured.DateOfBirth)
	if err != nil {
		return fmt.Errorf("insured.date_of_birth: %w", err)
	}
	if age := ageOn(dob, admission); age != s.Insured.Age {
		return fmt.Errorf("insured.date_of_birth %s gives age %d at the admission, but insured.age is %d", dob, age, s.Insured.Age)
	}
	return nil
}

// ageOn is the age in whole years on day d of someone born on dob.
func ageOn(dob, d rules.Date) int {
	age := d.Year() - dob.Year()
	if d.Month() < dob.Month() || (d.Month() == dob.Month() && d.Day() < dob.Day()) {
		age--
	}
	return age
}

// validateSurgery checks what OT notes print: the operation, on a day of
// the stay.
func (s Spec) validateSurgery(facts rules.Facts, needed bool) error {
	sg := s.Clinical.Surgery
	if sg == nil {
		if needed {
			return errors.New("OT notes need clinical.surgery")
		}
		return nil
	}
	if s.Clinical.Procedure == "" || sg.Surgeon == "" || sg.Anaesthetist == "" || sg.Anaesthesia == "" || sg.Findings == "" {
		return errors.New("clinical.surgery needs clinical.procedure, a surgeon, an anaesthetist, the anaesthesia and the findings")
	}
	d, err := rules.ParseDate(sg.Date)
	if err != nil {
		return fmt.Errorf("clinical.surgery.date: %w", err)
	}
	if d.Before(facts.Admission) || d.After(facts.Discharge) {
		return fmt.Errorf("clinical.surgery.date %s is outside the stay", d)
	}
	return nil
}

func checkAnnexure(l rules.Line, lf LineFormat) error {
	if lf.Annexure == "" {
		switch {
		case l.ItemList != "":
			return fmt.Errorf("line %s matches %s, so it needs an annexure item", l.ID, l.ItemList)
		case lf.MatchedBy != MatchedByName:
			return fmt.Errorf("line %s: matched_by needs an annexure item", l.ID)
		}
		return nil
	}
	item, ok := annexureItem(lf.Annexure)
	switch {
	case !ok:
		return fmt.Errorf("line %s: annexure item %q isn't in the generator's table", l.ID, lf.Annexure)
	case item.List != l.ItemList:
		return fmt.Errorf("line %s: annexure item %s is on %s, but the facts say %q", l.ID, lf.Annexure, item.List, l.ItemList)
	}
	switch lf.MatchedBy {
	case MatchedByName:
		if lf.Description != item.Name {
			return fmt.Errorf("line %s: description %q isn't the wording's %q", l.ID, lf.Description, item.Name)
		}
	case MatchedByExplanation, MatchedByStay:
		if lf.Description == item.Name {
			return fmt.Errorf("line %s prints the item's name, so it has no matched_by", l.ID)
		}
	default:
		return fmt.Errorf("line %s: matched_by %q isn't explanation or stay", l.ID, lf.MatchedBy)
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// maxLinesPerPage is what a bill page's table can hold.
const maxLinesPerPage = 24
