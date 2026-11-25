package datagen

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/signintech/gopdf"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// Packet is a rendered scenario: its files, in the spec's order, and its
// truth.
type Packet struct {
	Files []RenderedFile
	Truth Truth
}

// RenderedFile is one PDF.
type RenderedFile struct {
	Name string
	Data []byte
}

// Footer is printed at the foot of every page (ADR 0006).
const Footer = "SYNTHETIC TEST DOCUMENT · every name and number on it is invented"

// Font sizes and the table's geometry, in points.
const (
	sizeHospital = 14.0
	sizeTitle    = 12.0
	sizeBody     = 9.0
	sizeTable    = 8.5
	sizeFooter   = 7.5
	tableTop     = 196.0 // the column titles' baseline
	rowHeight    = 16.0
	cellPadding  = 2.0
	labelX       = 330.0 // where the right-hand header fields and total labels start
)

// Render draws a validated spec's packet. The same spec and Version always
// give the same bytes.
func Render(s Spec) (Packet, error) {
	if err := s.Validate(); err != nil {
		return Packet{}, err
	}
	r, err := newRenderer(s)
	if err != nil {
		return Packet{}, err
	}
	var p Packet
	for _, f := range s.Files {
		data, tf, err := r.file(f)
		if err != nil {
			return Packet{}, fmt.Errorf("render %s: %w", f.Name, err)
		}
		p.Files = append(p.Files, RenderedFile{Name: f.Name, Data: data})
		r.truth.Files = append(r.truth.Files, tf)
	}
	p.Truth = r.truth
	return p, nil
}

type renderer struct {
	spec  Spec
	facts map[string]rules.Line
	docs  map[string]Document
	// billLines are each bill's lines in the spec's order.
	billLines map[string][]LineFormat
	// gross is each bill's lines' sum.
	gross map[string]int64
	truth Truth
}

func newRenderer(s Spec) (*renderer, error) {
	_, _, facts, err := s.Engine()
	if err != nil {
		return nil, err
	}
	r := &renderer{
		spec:      s,
		facts:     map[string]rules.Line{},
		docs:      map[string]Document{},
		billLines: map[string][]LineFormat{},
		gross:     map[string]int64{},
	}
	for _, l := range facts.Lines {
		r.facts[l.ID] = l
	}
	for _, d := range s.Documents {
		r.docs[d.ID] = d
	}
	for _, lf := range s.Lines {
		r.billLines[lf.Bill] = append(r.billLines[lf.Bill], lf)
		amount := int64(r.facts[lf.Line].Amount)
		if r.gross[lf.Bill] > (1<<62)-amount {
			return nil, fmt.Errorf("bill %s: the lines' sum overflows", lf.Bill)
		}
		r.gross[lf.Bill] += amount
	}
	for _, d := range s.Documents {
		if d.IsBill() && d.Discount > r.gross[d.ID] {
			return nil, fmt.Errorf("bill %s: discount %d paise is more than its lines' %d", d.ID, d.Discount, r.gross[d.ID])
		}
	}
	types := map[DocumentType]bool{}
	for _, d := range s.Documents {
		types[d.Type] = true
	}
	for t := range types {
		r.truth.Documents = append(r.truth.Documents, t)
	}
	slices.Sort(r.truth.Documents)
	cl := s.Facts.Condition
	r.truth = Truth{
		GeneratorVersion: Version,
		Scenario:         s.ID,
		GoldenCase:       s.GoldenCase,
		Documents:        r.truth.Documents,
		Bills:            []TruthBill{},
		Receipts:         []TruthReceipt{},
		Claim: TruthClaim{
			Admission: s.Facts.Admission, Discharge: s.Facts.Discharge, Segments: s.Facts.Segments,
			Diagnosis: s.Clinical.Diagnosis, Procedure: s.Clinical.Procedure, TreatingDoctor: s.Clinical.TreatingDoctor,
			Accident: cl.Accident, Onset: cl.Onset, ListedGroup: cl.ListedGroup, PreExisting: cl.PreExisting,
		},
		Engine: TruthEngineInput{Policy: s.Policy, Cover: s.Cover, Facts: s.Facts},
	}
	return r, nil
}

func (r *renderer) net(bill string) int64 { return r.gross[bill] - r.docs[bill].Discount }

// file draws one file's documents in order.
func (r *renderer) file(f FileSpec) ([]byte, TruthFile, error) {
	pdf, err := newPDF()
	if err != nil {
		return nil, TruthFile{}, err
	}
	pg := &pager{pdf: pdf}
	tf := TruthFile{Name: f.Name}
	for _, id := range f.Documents {
		d := r.docs[id]
		first := pg.page + 1
		switch d.Type {
		case DocHospitalBill, DocPharmacyBill:
			err = r.bill(pg, f.Name, d)
		case DocPaymentReceipt:
			err = r.receipt(pg, f.Name, d)
		}
		if err != nil {
			return nil, TruthFile{}, fmt.Errorf("document %s: %w", id, err)
		}
		for p := first; p <= pg.page; p++ {
			tf.Pages = append(tf.Pages, TruthPage{Page: p, Type: d.Type, Document: id})
		}
		tf.Parts = append(tf.Parts, TruthPart{Document: id, Type: d.Type, FirstPage: first, LastPage: pg.page})
	}
	data, err := pdf.GetBytesPdfReturnErr()
	if err != nil {
		return nil, TruthFile{}, err
	}
	sum := sha256.Sum256(data)
	tf.SHA256 = hex.EncodeToString(sum[:])
	return data, tf, nil
}

func (r *renderer) bill(pg *pager, file string, d Document) error {
	st := StyleOf(d)
	lines := r.billLines[d.ID]
	pages := (len(lines) + d.LinesPerPage - 1) / d.LinesPerPage
	date, err := rules.ParseDate(d.Date)
	if err != nil {
		return err
	}
	tb := TruthBill{
		Document: d.ID, Type: d.Type, Layout: d.Layout, File: file,
		FirstPage: pg.page + 1, LastPage: pg.page + pages,
		Number:  d.Number,
		Date:    PrintedDate{Printed: FormatDate(date, st.Dates), Date: d.Date},
		Patient: r.spec.Insured.Name,
		Lines:   []TruthLine{},
	}
	for i := range pages {
		pg.add()
		if err := r.billHeader(pg, d, st, tb, i+1, pages); err != nil {
			return err
		}
		y := tableTop
		for _, c := range st.Columns {
			if err := pg.cell(fontBold, sizeTable, c, y, c.Title); err != nil {
				return err
			}
		}
		pg.rule(y + 5)
		y += 5
		chunk := lines[i*d.LinesPerPage : min((i+1)*d.LinesPerPage, len(lines))]
		for j, lf := range chunk {
			y += rowHeight
			tl, err := r.row(pg, st, lf, i*d.LinesPerPage+j+1, y)
			if err != nil {
				return err
			}
			tb.Lines = append(tb.Lines, tl)
		}
		if i == pages-1 {
			if tb.Totals, err = r.totals(pg, st, d, y+8); err != nil {
				return err
			}
		}
	}
	r.truth.Bills = append(r.truth.Bills, tb)
	return nil
}

func (r *renderer) billHeader(pg *pager, d Document, st Style, tb TruthBill, page, pages int) error {
	if err := r.hospitalHeader(pg); err != nil {
		return err
	}
	if err := pg.right(fontBold, sizeBody, marginRight, 50, fmt.Sprintf("Page %d of %d", page, pages)); err != nil {
		return err
	}
	title := "IN-PATIENT FINAL BILL"
	if d.Type == DocPharmacyBill {
		title = "PHARMACY BILL"
	}
	in := r.spec.Insured
	fields := [][2]string{
		{"Bill No: " + tb.Number, "Bill Date: " + tb.Date.Printed},
		{"Patient: " + tb.Patient, fmt.Sprintf("Age / Sex: %d / %s", in.Age, capitalize(in.Gender))},
		{"Policy No: " + in.PolicyNumber, "Member ID: " + in.MemberID},
	}
	if d.Type == DocHospitalBill {
		adm, _ := rules.ParseDate(r.spec.Facts.Admission)
		dis, _ := rules.ParseDate(r.spec.Facts.Discharge)
		fields = append(fields, [2]string{"Admitted: " + FormatDate(adm, st.Dates), "Discharged: " + FormatDate(dis, st.Dates)})
	}
	if err := pg.text(fontBold, sizeTitle, marginLeft, 108, title); err != nil {
		return err
	}
	y := 126.0
	for _, f := range fields {
		if err := pg.text(fontRegular, sizeBody, marginLeft, y, f[0]); err != nil {
			return err
		}
		if err := pg.text(fontRegular, sizeBody, labelX, y, f[1]); err != nil {
			return err
		}
		y += 14
	}
	return nil
}

func (r *renderer) hospitalHeader(pg *pager) error {
	h := r.spec.Hospital
	if err := pg.text(fontBold, sizeHospital, marginLeft, 50, h.Name); err != nil {
		return err
	}
	if err := pg.text(fontRegular, sizeBody, marginLeft, 66, h.Address+", "+h.City); err != nil {
		return err
	}
	if err := pg.text(fontRegular, sizeBody, marginLeft, 79, "Regd. No. "+h.Registration); err != nil {
		return err
	}
	pg.rule(88)
	return pg.footer()
}

// row draws one line's cells on the baseline y and returns its truth.
func (r *renderer) row(pg *pager, st Style, lf LineFormat, serial int, y float64) (TruthLine, error) {
	l := r.facts[lf.Line]
	tl := TruthLine{
		Line:        l.ID,
		Page:        pg.page,
		Description: lf.Description,
		Annexure:    lf.Annexure,
		MatchedBy:   lf.MatchedBy,
		Quantity:    PrintedInteger{Printed: itoa(l.Quantity), Value: l.Quantity},
		UnitPrice:   PrintedAmount{Printed: st.Money(int64(l.UnitPrice)), Paise: int64(l.UnitPrice)},
		Amount:      PrintedAmount{Printed: st.Money(int64(l.Amount)), Paise: int64(l.Amount)},
	}
	if !l.ServiceDate.IsZero() {
		tl.ServiceDate = &PrintedDate{Printed: FormatDate(l.ServiceDate, st.Dates), Date: l.ServiceDate.String()}
	}
	for _, c := range st.Columns {
		var v string
		switch c.Field {
		case FieldSerial:
			v = itoa(int64(serial))
		case FieldDate:
			if tl.ServiceDate != nil {
				v = tl.ServiceDate.Printed
			}
		case FieldDescription:
			v = tl.Description
		case FieldQuantity:
			v = tl.Quantity.Printed
		case FieldUnitPrice:
			v = tl.UnitPrice.Printed
		case FieldAmount:
			v = tl.Amount.Printed
		}
		if err := pg.cell(fontRegular, sizeTable, c, y, v); err != nil {
			return TruthLine{}, fmt.Errorf("line %s: %w", l.ID, err)
		}
	}
	return tl, nil
}

// totals draws a bill's totals below its last row.
func (r *renderer) totals(pg *pager, st Style, d Document, y float64) (TruthTotals, error) {
	pg.rule(y)
	gross := r.gross[d.ID]
	t := TruthTotals{
		Page:  pg.page,
		Gross: PrintedAmount{Printed: st.Total(gross), Paise: gross},
		Net:   PrintedAmount{Printed: st.Total(r.net(d.ID)), Paise: r.net(d.ID)},
	}
	type totalRow struct {
		label string
		value PrintedAmount
		font  string
	}
	rows := []totalRow{{"Gross Amount", t.Gross, fontRegular}}
	if d.Discount > 0 {
		t.Discount = &PrintedAmount{Printed: st.Total(d.Discount), Paise: d.Discount}
		rows = append(rows, totalRow{"Less: Discount", *t.Discount, fontRegular})
	}
	rows = append(rows, totalRow{"Net Amount Payable", t.Net, fontBold})
	for _, row := range rows {
		y += rowHeight
		if err := pg.text(row.font, sizeBody, labelX, y, row.label); err != nil {
			return TruthTotals{}, err
		}
		if err := pg.right(row.font, sizeBody, marginRight, y, row.value.Printed); err != nil {
			return TruthTotals{}, err
		}
	}
	return t, nil
}

func (r *renderer) receipt(pg *pager, file string, d Document) error {
	st := StyleOf(d)
	date, err := rules.ParseDate(d.Date)
	if err != nil {
		return err
	}
	var amount int64
	var numbers []string
	for _, b := range d.Against {
		amount += r.net(b)
		numbers = append(numbers, r.docs[b].Number)
	}
	pg.add()
	if err := r.hospitalHeader(pg); err != nil {
		return err
	}
	tr := TruthReceipt{
		Document: d.ID, File: file, Page: pg.page, Number: d.Number,
		Date:    PrintedDate{Printed: FormatDate(date, st.Dates), Date: d.Date},
		Amount:  PrintedAmount{Printed: st.Total(amount), Paise: amount},
		Against: d.Against,
	}
	if err := pg.text(fontBold, sizeTitle, marginLeft, 108, "PAYMENT RECEIPT"); err != nil {
		return err
	}
	fields := []string{
		"Receipt No: " + tr.Number,
		"Date: " + tr.Date.Printed,
		"Received from: " + r.spec.Insured.Name,
		"Amount received: " + tr.Amount.Printed,
		"Towards bill(s): " + strings.Join(numbers, ", "),
		"Mode of payment: " + d.Mode,
	}
	y := 126.0
	for _, f := range fields {
		if err := pg.text(fontRegular, sizeBody, marginLeft, y, f); err != nil {
			return err
		}
		y += 16
	}
	if err := pg.text(fontRegular, sizeBody, labelX, y+40, "Authorised Signatory"); err != nil {
		return err
	}
	r.truth.Receipts = append(r.truth.Receipts, tr)
	return nil
}

// pager draws on the current page and counts pages within the file.
type pager struct {
	pdf  *gopdf.GoPdf
	page int
}

func (p *pager) add() {
	p.pdf.AddPage()
	p.page++
}

func (p *pager) text(font string, size, x, y float64, s string) error {
	if err := p.pdf.SetFont(font, "", size); err != nil {
		return err
	}
	w, err := p.pdf.MeasureTextWidth(s)
	if err != nil {
		return err
	}
	if x+w > marginRight+0.01 {
		return fmt.Errorf("%q runs past the right margin", s)
	}
	p.pdf.SetXY(x, y)
	return p.pdf.Text(s)
}

func (p *pager) right(font string, size, xRight, y float64, s string) error {
	if err := p.pdf.SetFont(font, "", size); err != nil {
		return err
	}
	w, err := p.pdf.MeasureTextWidth(s)
	if err != nil {
		return err
	}
	p.pdf.SetXY(xRight-w, y)
	return p.pdf.Text(s)
}

// errTooWide means a value doesn't fit its column. A row never wraps, so
// the spec has to shorten the value.
var errTooWide = errors.New("doesn't fit its column")

// cell draws a value in a column, failing if it doesn't fit.
func (p *pager) cell(font string, size float64, c Column, y float64, s string) error {
	if s == "" {
		return nil
	}
	if err := p.pdf.SetFont(font, "", size); err != nil {
		return err
	}
	w, err := p.pdf.MeasureTextWidth(s)
	if err != nil {
		return err
	}
	if w > c.Width-cellPadding {
		return fmt.Errorf("%s %q (%.1f points, column %.1f): %w", c.Field, s, w, c.Width, errTooWide)
	}
	x := c.X
	if c.Right {
		x = c.X + c.Width - w
	}
	p.pdf.SetXY(x, y)
	return p.pdf.Text(s)
}

func (p *pager) rule(y float64) {
	p.pdf.SetLineWidth(0.5)
	p.pdf.Line(marginLeft, y, marginRight, y)
}

func (p *pager) footer() error {
	if err := p.pdf.SetFont(fontRegular, "", sizeFooter); err != nil {
		return err
	}
	w, err := p.pdf.MeasureTextWidth(Footer)
	if err != nil {
		return err
	}
	p.pdf.SetXY((pageWidth-w)/2, pageHeight-24)
	return p.pdf.Text(Footer)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
