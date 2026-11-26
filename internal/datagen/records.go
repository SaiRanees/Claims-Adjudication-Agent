package datagen

import (
	"fmt"
	"strconv"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// The documents that aren't bills or receipts: the claim form, the photo
// ID, the discharge summary, the OT notes and the implant invoice. Each is
// drawn through a record, which keeps the first error and collects the
// document's truth: every value it prints, on the page it prints it.

// Their styles, which StyleOf returns. The ID card and the invoice print dates with month names,
// and the invoice groups digits in threes, so a packet's documents vary as
// its bills do.
var (
	claimFormStyle = Style{Grouping: GroupIndian, Dates: DateSlashes, Mark: "₹"}
	photoIDStyle   = Style{Grouping: GroupIndian, Dates: DateMonthName}
	clinicalStyle  = Style{Grouping: GroupIndian, Dates: DateSlashes}
	invoiceStyle   = Style{Grouping: GroupWestern, Dates: DateMonthName, Mark: "Rs. ", Columns: []Column{
		{FieldSerial, "#", 40, 20, false},
		{FieldDescription, "Item", 64, 190, false},
		{FieldSerialNumber, "Serial No.", 258, 96, false},
		{FieldQuantity, "Qty", 358, 30, true},
		{FieldUnitPrice, "Rate (Rs.)", 392, 80, true},
		{FieldAmount, "Amount (Rs.)", 476, 79, true},
	}}
)

// The distances between a record's lines and a paragraph's, in points.
const (
	lead          = 16.0
	paragraphLead = 13.0
)

type record struct {
	r   *renderer
	pg  *pager
	st  Style
	td  TruthDocument
	err error
}

func (r *renderer) newRecord(pg *pager, file string, d Document) *record {
	st := StyleOf(d)
	rc := &record{r: r, pg: pg, st: st, td: TruthDocument{
		Document: d.ID, Type: d.Type, File: file, FirstPage: pg.page + 1,
		Number: d.Number, Patient: r.spec.Insured.Name, Fields: []TruthField{},
	}}
	date, err := rules.ParseDate(d.Date)
	rc.err = err
	rc.td.Date = PrintedDate{Printed: FormatDate(date, st.Dates), Date: d.Date}
	return rc
}

// done records the document's truth and returns the first error.
func (rc *record) done() error {
	if rc.err != nil {
		return rc.err
	}
	rc.td.LastPage = rc.pg.page
	rc.r.truth.Other = append(rc.r.truth.Other, rc.td)
	return nil
}

// do runs a drawing step unless one has failed.
func (rc *record) do(f func() error) {
	if rc.err == nil {
		rc.err = f()
	}
}

// say prints text that isn't a recorded value.
func (rc *record) say(font string, size, x, y float64, s string) {
	rc.do(func() error { return rc.pg.text(font, size, x, y, s) })
}

// keep records a field printed on the current page.
func (rc *record) keep(f TruthField) {
	f.Page = rc.pg.page
	rc.td.Fields = append(rc.td.Fields, f)
}

// put prints label and the field's value as one string and records the
// field.
func (rc *record) put(x, y float64, label string, f TruthField) {
	rc.do(func() error { return rc.pg.text(fontRegular, sizeBody, x, y, label+f.Printed) })
	if rc.err == nil {
		rc.keep(f)
	}
}

// putRight prints the field's value right-aligned at xRight, in bold or
// not, and records it.
func (rc *record) putRight(font string, xRight, y float64, f TruthField) {
	rc.do(func() error { return rc.pg.right(font, sizeBody, xRight, y, f.Printed) })
	if rc.err == nil {
		rc.keep(f)
	}
}

// paragraph prints a wrapped paragraph and returns the baseline below it.
func (rc *record) paragraph(y float64, s string) float64 {
	rc.do(func() error {
		var err error
		y, err = rc.pg.paragraph(fontRegular, sizeBody, marginLeft, y, marginRight-marginLeft, paragraphLead, s)
		return err
	})
	return y
}

// heading prints a section's heading in bold.
func (rc *record) heading(y float64, s string) { rc.say(fontBold, sizeBody, marginLeft, y, s) }

// marker prints "Page x of y" at the top right.
func (rc *record) marker(page, pages int) {
	rc.do(func() error {
		return rc.pg.right(fontBold, sizeBody, marginRight, 50, fmt.Sprintf("Page %d of %d", page, pages))
	})
}

// idRows prints the number, the date and the patient, which every page of
// a record carries, from baseline y, and returns the baseline below them.
func (rc *record) idRows(y float64, numberLabel, patientLabel string) float64 {
	in := rc.r.spec.Insured
	rc.say(fontRegular, sizeBody, marginLeft, y, numberLabel+": "+rc.td.Number)
	rc.say(fontRegular, sizeBody, labelX, y, "Date: "+rc.td.Date.Printed)
	rc.say(fontRegular, sizeBody, marginLeft, y+14, patientLabel+": "+rc.td.Patient)
	rc.say(fontRegular, sizeBody, labelX, y+14, fmt.Sprintf("Age / Sex: %d / %s", in.Age, capitalize(in.Gender)))
	return y + 14 + lead + 8
}

func field(name, s string) TruthField { return TruthField{Name: name, Printed: s, Value: s} }

func yesNo(name string, b bool) TruthField {
	printed := "No"
	if b {
		printed = "Yes"
	}
	return TruthField{Name: name, Printed: printed, Value: strconv.FormatBool(b)}
}

func (rc *record) date(name, iso string) TruthField {
	d, err := rules.ParseDate(iso)
	if err != nil && rc.err == nil {
		rc.err = fmt.Errorf("%s: %w", name, err)
	}
	return TruthField{Name: name, Printed: FormatDate(d, rc.st.Dates), Value: iso}
}

func (rc *record) amount(name string, p int64) TruthField {
	return TruthField{Name: name, Printed: rc.st.Total(p), Value: itoa(p)}
}

func (r *renderer) admissionType() string { return capitalize(r.spec.ClaimForm.AdmissionType) }

// claimForm draws the claim form over two pages: the insured person and
// the hospitalisation, then the claim and the declaration.
func (r *renderer) claimForm(pg *pager, file string, d Document) error {
	rc := r.newRecord(pg, file, d)
	s := r.spec
	page := func(n int) float64 {
		pg.add()
		rc.do(pg.footer)
		rc.say(fontBold, sizeTitle, marginLeft, 50, "HEALTH INSURANCE CLAIM FORM - REIMBURSEMENT")
		rc.say(fontRegular, sizeBody, marginLeft, 66, "Arogya Sanjeevani Policy · to be filled in by the insured person")
		rc.marker(n, 2)
		pg.rule(78)
		return rc.idRows(98, "Form No", "Insured person")
	}

	y := page(1)
	rc.heading(y, "PART A - DETAILS OF THE INSURED PERSON")
	y += lead + 2
	rc.put(marginLeft, y, "Policy No: ", field("policy_number", s.Insured.PolicyNumber))
	rc.put(labelX, y, "Member ID: ", field("member_id", s.Insured.MemberID))
	y += lead
	rc.put(marginLeft, y, "Gender: ", field("gender", capitalize(s.Insured.Gender)))
	if s.Insured.DateOfBirth != "" {
		rc.put(labelX, y, "Date of birth: ", rc.date("date_of_birth", s.Insured.DateOfBirth))
	}
	y += lead + 12
	rc.heading(y, "PART B - DETAILS OF HOSPITALISATION")
	y += lead + 2
	rc.put(marginLeft, y, "Hospital: ", field("hospital", s.Hospital.Name))
	y += lead
	rc.put(marginLeft, y, "City: ", field("hospital_city", s.Hospital.City))
	rc.put(labelX, y, "Admission type: ", field("admission_type", r.admissionType()))
	y += lead
	rc.put(marginLeft, y, "Date of admission: ", rc.date("admission", s.Facts.Admission))
	rc.put(labelX, y, "Date of discharge: ", rc.date("discharge", s.Facts.Discharge))
	y += lead
	rc.put(marginLeft, y, "Diagnosis: ", field("diagnosis", s.Clinical.Diagnosis))
	y += lead
	if s.Clinical.Procedure != "" {
		rc.put(marginLeft, y, "Procedure: ", field("procedure", s.Clinical.Procedure))
		y += lead
	}
	rc.put(marginLeft, y, "Treating doctor: ", field("treating_doctor", s.Clinical.TreatingDoctor))
	rc.put(labelX, y, "Caused by an accident: ", yesNo("accident", s.Facts.Condition.Accident))

	y = page(2)
	rc.heading(y, "PART C - CLAIM DETAILS")
	y += lead + 2
	rc.put(marginLeft, y, "Total amount claimed: ", rc.amount("claimed_amount", s.ClaimForm.ClaimedAmount))
	y += lead + 6
	rc.say(fontBold, sizeBody, marginLeft, y, "Bills enclosed")
	rc.say(fontBold, sizeBody, 230, y, "Bill date")
	rc.do(func() error { return pg.right(fontBold, sizeBody, 420, y, "Net amount") })
	for _, b := range s.Documents {
		if !b.IsBill() {
			continue
		}
		y += lead
		number, date, net := field("bill_number", b.Number), rc.date("bill_date", b.Date), rc.amount("bill_amount", r.net(b.ID))
		number.Row, date.Row, net.Row = b.ID, b.ID, b.ID
		rc.put(marginLeft, y, "", number)
		rc.put(230, y, "", date)
		rc.putRight(fontRegular, 420, y, net)
	}
	y += lead + 12
	rc.heading(y, "DECLARATION")
	y = rc.paragraph(y+lead, "I declare that the information in this form is true and complete, that no other claim "+
		"has been made for this hospitalisation, and that the documents enclosed are the originals. "+
		"This form is a synthetic test document: every name and number on it is invented.")
	y += lead
	rc.put(marginLeft, y, "Signed on: ", rc.date("signed_on", s.ClaimForm.SignedOn))
	rc.say(fontRegular, sizeBody, labelX, y, "Signature of the insured person")
	return rc.done()
}

// photoID draws a copy of an invented identity card. It follows no real
// ID's design: a plain bordered card from an invented registry that says it
// isn't issued by any government, with no emblem or seal, and a number
// shaped SYN-ID-XXXX-9999, which validation enforces.
func (r *renderer) photoID(pg *pager, file string, d Document) error {
	rc := r.newRecord(pg, file, d)
	in := r.spec.Insured
	pg.add()
	rc.do(pg.footer)
	rc.say(fontBold, sizeTitle, marginLeft, 50, "COPY OF PHOTO IDENTITY CARD")
	rc.say(fontRegular, sizeBody, marginLeft, 66, "Submitted with the claim as proof of identity")

	const x, y, w, h = 80.0, 90.0, 320.0, 180.0
	pg.box(x, y, w, h)
	rc.say(fontBold, 11, x+12, y+20, "SYNTHETIC IDENTITY CARD")
	rc.say(fontRegular, sizeFooter, x+12, y+33, "Synthetic Test Registry · not issued by any government")
	pg.ruleBetween(x, x+w, y+40)
	// The photo: a frame holding the outline of a head and shoulders.
	pg.box(x+12, y+50, 70, 88)
	pg.pdf.Oval(x+31, y+60, x+63, y+96)
	pg.pdf.Oval(x+20, y+100, x+74, y+134)
	rc.say(fontRegular, 6.5, x+36, y+150, "PHOTO")

	fx := x + 100
	rc.put(fx, y+64, "Name: ", field("name", in.Name))
	rc.put(fx, y+82, "Date of birth: ", rc.date("date_of_birth", in.DateOfBirth))
	rc.put(fx, y+100, "Gender: ", field("gender", capitalize(in.Gender)))
	rc.say(fontRegular, sizeBody, fx, y+118, "ID No: "+d.Number)
	rc.say(fontRegular, sizeBody, fx, y+136, "Issued on: "+rc.td.Date.Printed)

	rc.say(fontRegular, sizeBody, marginLeft, y+h+30, "Self-attested copy")
	rc.say(fontRegular, sizeBody, labelX, y+h+30, "Signature: "+in.Name)
	return rc.done()
}

// hospitalPage starts a page of a hospital's record: the hospital's
// header, the title, the page marker when there's more than one page, and
// the record's number, date and patient. It returns the next baseline.
func (rc *record) hospitalPage(title, numberLabel string, page, pages int) float64 {
	rc.pg.add()
	rc.do(func() error { return rc.r.hospitalHeader(rc.pg) })
	if pages > 1 {
		rc.marker(page, pages)
	}
	rc.say(fontBold, sizeTitle, marginLeft, 108, title)
	return rc.idRows(126, numberLabel, "Patient")
}

// segmentLabels are how the discharge summary names a segment's kind.
var segmentLabels = map[string]string{string(rules.SegmentRoom): "Ward", string(rules.SegmentICU): "ICU"}

// dischargeSummary draws the discharge summary over two pages: the stay
// and the diagnosis, then the course, the operation and the advice.
func (r *renderer) dischargeSummary(pg *pager, file string, d Document) error {
	rc := r.newRecord(pg, file, d)
	s := r.spec
	c, cond := s.Clinical, s.Facts.Condition

	y := rc.hospitalPage("DISCHARGE SUMMARY", "DS No", 1, 2)
	rc.put(marginLeft, y, "Date of admission: ", rc.date("admission", s.Facts.Admission))
	rc.put(labelX, y, "Date of discharge: ", rc.date("discharge", s.Facts.Discharge))
	y += lead
	rc.put(marginLeft, y, "Treating doctor: ", field("treating_doctor", c.TreatingDoctor))
	rc.put(labelX, y, "Admission type: ", field("admission_type", r.admissionType()))
	y += lead
	rc.put(marginLeft, y, "Diagnosis: ", field("diagnosis", c.Diagnosis))
	y += lead
	if c.Procedure != "" {
		rc.put(marginLeft, y, "Procedure: ", field("procedure", c.Procedure))
		y += lead
	}
	rc.put(marginLeft, y, "Injury from an accident: ", yesNo("accident", cond.Accident))
	if cond.Onset != "" {
		rc.put(labelX, y, "Onset of symptoms: ", rc.date("onset", cond.Onset))
	}
	y += lead + 8
	rc.heading(y, "Stay")
	for i, seg := range s.Facts.Segments {
		y += lead
		row := fmt.Sprintf("segment-%d", i+1)
		kind := TruthField{Name: "segment_kind", Printed: segmentLabels[seg.Kind], Value: seg.Kind}
		from, to := rc.date("segment_from", seg.From), rc.date("segment_to", seg.To)
		kind.Row, from.Row, to.Row = row, row, row
		rc.put(marginLeft, y, "", kind)
		rc.put(110, y, "from ", from)
		rc.put(230, y, "to ", to)
	}
	if c.History != "" {
		y += lead + 8
		rc.heading(y, "History and presenting complaints")
		rc.paragraph(y+lead, c.History)
	}

	y = rc.hospitalPage("DISCHARGE SUMMARY", "DS No", 2, 2)
	rc.heading(y, "Course in hospital")
	y = rc.paragraph(y+lead, c.Course)
	if c.Surgery != nil {
		y += 8
		rc.put(marginLeft, y, "Operated on ", rc.date("surgery_date", c.Surgery.Date))
		y += lead
	}
	y += 8
	rc.say(fontRegular, sizeBody, marginLeft, y, "Condition at discharge: Stable")
	y += lead + 8
	rc.heading(y, "Advice on discharge")
	y = rc.paragraph(y+lead, c.Advice)
	y += lead
	rc.say(fontRegular, sizeBody, labelX, y, c.TreatingDoctor)
	rc.say(fontRegular, sizeBody, labelX, y+14, "Treating Consultant")
	return rc.done()
}

// implantItem is an implant invoice's item with the description its line
// prints on the hospital bill.
type implantItem struct {
	Implant
	Description string
}

// implants lists the implant invoices' items in the spec's order.
func (r *renderer) implants() []implantItem {
	desc := map[string]string{}
	for _, lf := range r.spec.Lines {
		desc[lf.Line] = lf.Description
	}
	var out []implantItem
	for _, d := range r.spec.Documents {
		for _, im := range d.Implants {
			out = append(out, implantItem{im, desc[im.Line]})
		}
	}
	return out
}

// otNotes draws the operation's notes on one page, with each implant's
// serial number.
func (r *renderer) otNotes(pg *pager, file string, d Document) error {
	rc := r.newRecord(pg, file, d)
	c := r.spec.Clinical
	sg := c.Surgery

	y := rc.hospitalPage("OPERATION THEATRE NOTES", "OT No", 1, 1)
	rc.put(marginLeft, y, "Date of surgery: ", rc.date("surgery_date", sg.Date))
	y += lead
	rc.put(marginLeft, y, "Procedure: ", field("procedure", c.Procedure))
	y += lead
	rc.put(marginLeft, y, "Diagnosis: ", field("diagnosis", c.Diagnosis))
	y += lead
	rc.put(marginLeft, y, "Surgeon: ", field("surgeon", sg.Surgeon))
	rc.put(labelX, y, "Anaesthetist: ", field("anaesthetist", sg.Anaesthetist))
	y += lead
	rc.put(marginLeft, y, "Anaesthesia: ", field("anaesthesia", sg.Anaesthesia))
	y += lead + 8
	rc.heading(y, "Findings")
	y = rc.paragraph(y+lead, sg.Findings)
	y += 8
	rc.heading(y, "Implants used")
	items := r.implants()
	if len(items) == 0 {
		y += lead
		rc.say(fontRegular, sizeBody, marginLeft, y, "None")
	}
	for _, im := range items {
		y += lead
		desc, serial := field("implant", im.Description), field("implant_serial", im.Serial)
		desc.Row, desc.Line, serial.Row, serial.Line = im.Line, im.Line, im.Line, im.Line
		rc.put(marginLeft, y, "", desc)
		rc.put(labelX, y, "Sl. No. ", serial)
	}
	y += lead + 24
	rc.say(fontRegular, sizeBody, labelX, y, sg.Surgeon)
	rc.say(fontRegular, sizeBody, labelX, y+14, "Operating Surgeon")
	return rc.done()
}

// implantInvoice draws the vendor's invoice for implants the hospital bill
// lists, at the bill's quantities and prices.
func (r *renderer) implantInvoice(pg *pager, file string, d Document) error {
	rc := r.newRecord(pg, file, d)
	st := invoiceStyle
	v := d.Vendor
	pg.add()
	rc.do(pg.footer)
	rc.say(fontBold, sizeHospital, marginLeft, 50, v.Name)
	rc.say(fontRegular, sizeBody, marginLeft, 66, v.Address)
	rc.say(fontRegular, sizeBody, marginLeft, 79, "Regd. No. "+v.Registration)
	pg.rule(88)
	rc.say(fontBold, sizeTitle, marginLeft, 108, "INVOICE")
	y := rc.idRows(126, "Invoice No", "Patient")
	rc.put(marginLeft, y, "Billed to: ", field("billed_to", r.spec.Hospital.Name))

	y = tableTop
	for _, c := range st.Columns {
		rc.do(func() error { return pg.cell(fontBold, sizeTable, c, y, c.Title) })
	}
	pg.rule(y + 5)
	y += 5
	desc := map[string]string{}
	for _, lf := range r.spec.Lines {
		desc[lf.Line] = lf.Description
	}
	var total int64
	for i, im := range d.Implants {
		y += rowHeight
		l := r.facts[im.Line]
		total += int64(l.Amount)
		values := map[ColumnField]TruthField{
			FieldSerial:       field("item", itoa(int64(i+1))),
			FieldDescription:  field("description", desc[im.Line]),
			FieldSerialNumber: field("serial", im.Serial),
			FieldQuantity:     {Name: "quantity", Printed: itoa(l.Quantity), Value: itoa(l.Quantity)},
			FieldUnitPrice:    {Name: "unit_price", Printed: st.Money(int64(l.UnitPrice)), Value: itoa(int64(l.UnitPrice))},
			FieldAmount:       {Name: "amount", Printed: st.Money(int64(l.Amount)), Value: itoa(int64(l.Amount))},
		}
		for _, c := range st.Columns {
			f := values[c.Field]
			rc.do(func() error { return pg.cell(fontRegular, sizeTable, c, y, f.Printed) })
			if c.Field != FieldSerial && rc.err == nil {
				f.Row, f.Line = im.Line, im.Line
				rc.keep(f)
			}
		}
	}
	pg.rule(y + 8)
	y += 8 + rowHeight
	rc.say(fontBold, sizeBody, labelX, y, "Invoice Total")
	rc.putRight(fontBold, marginRight, y, rc.amount("total", total))
	rc.say(fontRegular, sizeBody, marginLeft, y+40, "Supplied for implantation in the patient named above.")
	rc.say(fontRegular, sizeBody, labelX, y+40, "For "+v.Name)
	rc.say(fontRegular, sizeBody, labelX, y+54, "Authorised Signatory")
	return rc.done()
}
