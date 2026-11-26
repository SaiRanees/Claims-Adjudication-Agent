package datagen

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// PageReader reads each page's text from a PDF. documents.PDFium is one.
type PageReader interface {
	PageTexts(ctx context.Context, data []byte) ([]string, error)
}

// SelfCheck checks a rendered packet against its truth. CheckTruth runs
// first. Then every page is read back with rd, and every printed string the
// truth names (amounts, unit prices, quantities, dates, descriptions,
// totals, numbers and names) must appear on the page it names. A line's
// strings must all sit on one line of that page's text, so a value can't be
// satisfied by the same string elsewhere on the page, and so must a row of
// another document's fields, such as an implant invoice's item. Every page
// must carry the footer.
func SelfCheck(ctx context.Context, rd PageReader, p Packet) error {
	if err := CheckTruth(p); err != nil {
		return err
	}
	texts := map[string][]string{}
	var errs []error
	for _, f := range p.Files {
		pages, err := rd.PageTexts(ctx, f.Data)
		if err != nil {
			return fmt.Errorf("read %s: %w", f.Name, err)
		}
		texts[f.Name] = pages
	}
	for _, tf := range p.Truth.Files {
		pages := texts[tf.Name]
		if len(pages) != len(tf.Pages) {
			errs = append(errs, fmt.Errorf("%s: PDFium reads %d pages, truth has %d", tf.Name, len(pages), len(tf.Pages)))
			continue
		}
		for i, text := range pages {
			if !strings.Contains(text, Footer) {
				errs = append(errs, fmt.Errorf("%s page %d: no footer", tf.Name, i+1))
			}
		}
	}
	find := func(file string, page int, what, s string) {
		pages := texts[file]
		if page < 1 || page > len(pages) {
			errs = append(errs, fmt.Errorf("%s: %s is on page %d, which doesn't exist", file, what, page))
			return
		}
		if !strings.Contains(pages[page-1], s) {
			errs = append(errs, fmt.Errorf("%s page %d: %s %q isn't on the page", file, page, what, s))
		}
	}
	for _, b := range p.Truth.Bills {
		pages := b.LastPage - b.FirstPage + 1
		for pg := b.FirstPage; pg <= b.LastPage; pg++ {
			find(b.File, pg, b.Document+" number", b.Number)
			find(b.File, pg, b.Document+" date", b.Date.Printed)
			find(b.File, pg, b.Document+" patient", b.Patient)
			find(b.File, pg, b.Document+" page marker", fmt.Sprintf("Page %d of %d", pg-b.FirstPage+1, pages))
		}
		for _, l := range b.Lines {
			if err := findRow(texts[b.File], l); err != nil {
				errs = append(errs, fmt.Errorf("%s line %s: %w", b.File, l.Line, err))
			}
		}
		t := b.Totals
		find(b.File, t.Page, b.Document+" gross", t.Gross.Printed)
		if t.Discount != nil {
			find(b.File, t.Page, b.Document+" discount", t.Discount.Printed)
		}
		find(b.File, t.Page, b.Document+" net", t.Net.Printed)
	}
	for _, r := range p.Truth.Receipts {
		find(r.File, r.Page, r.Document+" number", r.Number)
		find(r.File, r.Page, r.Document+" date", r.Date.Printed)
		find(r.File, r.Page, r.Document+" amount", r.Amount.Printed)
	}
	for _, d := range p.Truth.Other {
		pages := d.LastPage - d.FirstPage + 1
		for pg := d.FirstPage; pg <= d.LastPage; pg++ {
			find(d.File, pg, d.Document+" number", d.Number)
			find(d.File, pg, d.Document+" date", d.Date.Printed)
			find(d.File, pg, d.Document+" patient", d.Patient)
			if pages > 1 {
				find(d.File, pg, d.Document+" page marker", fmt.Sprintf("Page %d of %d", pg-d.FirstPage+1, pages))
			}
		}
		rows := map[string][]TruthField{}
		for _, f := range d.Fields {
			find(d.File, f.Page, d.Document+" "+f.Name, f.Printed)
			if f.Row != "" {
				rows[f.Row] = append(rows[f.Row], f)
			}
		}
		for _, row := range sortedKeys(rows) {
			if err := findFields(texts[d.File], rows[row]); err != nil {
				errs = append(errs, fmt.Errorf("%s %s row %s: %w", d.File, d.Document, row, err))
			}
		}
	}
	return errors.Join(errs...)
}

// findFields looks for one text line holding all of a row's fields, which
// are on one page.
func findFields(pages []string, row []TruthField) error {
	page := row[0].Page
	want := make([]string, 0, len(row))
	for _, f := range row {
		if f.Page != page {
			return fmt.Errorf("its fields are on pages %d and %d", page, f.Page)
		}
		want = append(want, f.Printed)
	}
	if page < 1 || page > len(pages) {
		return fmt.Errorf("page %d doesn't exist", page)
	}
	for _, line := range splitLines(pages[page-1]) {
		if containsAll(line, want) {
			return nil
		}
	}
	return fmt.Errorf("no line of page %d holds all of %q", page, want)
}

// findRow looks for one text line on the line's page holding all its
// printed strings.
func findRow(pages []string, l TruthLine) error {
	if l.Page < 1 || l.Page > len(pages) {
		return fmt.Errorf("page %d doesn't exist", l.Page)
	}
	want := []string{l.Description, l.Quantity.Printed, l.UnitPrice.Printed, l.Amount.Printed}
	if l.ServiceDate != nil {
		want = append(want, l.ServiceDate.Printed)
	}
	for _, row := range splitLines(pages[l.Page-1]) {
		if containsAll(row, want) {
			return nil
		}
	}
	return fmt.Errorf("no line of page %d holds all of %q", l.Page, want)
}

func splitLines(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' })
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// CheckTruth checks the truth against itself and the spec's facts, without
// reading a page: the files and their hashes match the rendering, each
// file's pages and parts tile it, every facts line is printed exactly once
// with its facts' values, each bill's totals add up, the discounts add up
// to the facts' bill discount, and the other documents lie within their
// files and print their lines' values.
func CheckTruth(p Packet) error {
	tr := p.Truth
	var errs []error
	if tr.GeneratorVersion != Version {
		errs = append(errs, fmt.Errorf("generator version %q, want %q", tr.GeneratorVersion, Version))
	}
	if len(tr.Files) != len(p.Files) {
		return fmt.Errorf("truth has %d files, the packet %d", len(tr.Files), len(p.Files))
	}
	for i, tf := range tr.Files {
		if tf.Name != p.Files[i].Name || tf.SHA256 != sha256Hex(p.Files[i].Data) {
			errs = append(errs, fmt.Errorf("file %d: truth names %s %s", i, tf.Name, tf.SHA256))
		}
		next := 1
		for _, part := range tf.Parts {
			if part.FirstPage != next || part.LastPage < part.FirstPage {
				errs = append(errs, fmt.Errorf("%s: part %s doesn't start at page %d", tf.Name, part.Document, next))
			}
			for pg := part.FirstPage; pg <= part.LastPage && pg <= len(tf.Pages); pg++ {
				if got := tf.Pages[pg-1]; got.Page != pg || got.Document != part.Document || got.Type != part.Type {
					errs = append(errs, fmt.Errorf("%s page %d: %+v isn't part %s", tf.Name, pg, got, part.Document))
				}
			}
			next = part.LastPage + 1
		}
		if next-1 != len(tf.Pages) {
			errs = append(errs, fmt.Errorf("%s: parts cover %d pages of %d", tf.Name, next-1, len(tf.Pages)))
		}
	}

	_, _, facts, err := EngineInput(tr.Engine.Policy, tr.Engine.Cover, tr.Engine.Facts)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	printed := map[string]int{}
	var discounts int64
	for _, b := range tr.Bills {
		var gross int64
		for _, l := range b.Lines {
			printed[l.Line]++
			gross += l.Amount.Paise
			if l.Page < b.FirstPage || l.Page > b.LastPage {
				errs = append(errs, fmt.Errorf("bill %s line %s: page %d is outside the bill", b.Document, l.Line, l.Page))
			}
		}
		var discount int64
		if b.Totals.Discount != nil {
			discount = b.Totals.Discount.Paise
		}
		discounts += discount
		if b.Totals.Gross.Paise != gross || b.Totals.Net.Paise != gross-discount || b.Totals.Page != b.LastPage {
			errs = append(errs, fmt.Errorf("bill %s: totals %+v don't add up to its lines' %d", b.Document, b.Totals, gross))
		}
	}
	if discounts != int64(facts.BillDiscount) {
		errs = append(errs, fmt.Errorf("bills' discounts %d, facts' %d", discounts, facts.BillDiscount))
	}
	byID := map[string]TruthLine{}
	for _, b := range tr.Bills {
		for _, l := range b.Lines {
			byID[l.Line] = l
		}
	}
	for _, f := range facts.Lines {
		l, n := byID[f.ID], printed[f.ID]
		if n != 1 {
			errs = append(errs, fmt.Errorf("facts line %s is printed %d times", f.ID, n))
			continue
		}
		date := ""
		if l.ServiceDate != nil {
			date = l.ServiceDate.Date
		}
		wantDate := ""
		if !f.ServiceDate.IsZero() {
			wantDate = f.ServiceDate.String()
		}
		if l.Quantity.Value != f.Quantity || l.UnitPrice.Paise != int64(f.UnitPrice) || l.Amount.Paise != int64(f.Amount) || date != wantDate {
			errs = append(errs, fmt.Errorf("line %s is printed as %+v, but its facts are %+v", f.ID, l, f))
		}
	}
	if len(byID) != len(facts.Lines) {
		errs = append(errs, fmt.Errorf("the bills print %d lines, the facts have %d", len(byID), len(facts.Lines)))
	}
	errs = append(errs, checkOther(tr, facts)...)
	return errors.Join(errs...)
}

// checkOther checks the other documents' truth: each lies within its file,
// its fields lie within it, and a field that prints a facts line's
// quantity, unit price or amount prints that line's value.
func checkOther(tr Truth, facts rules.Facts) []error {
	var errs []error
	pages := map[string]int{}
	for _, tf := range tr.Files {
		pages[tf.Name] = len(tf.Pages)
	}
	lines := map[string]rules.Line{}
	for _, l := range facts.Lines {
		lines[l.ID] = l
	}
	for _, d := range tr.Other {
		if d.FirstPage < 1 || d.LastPage < d.FirstPage || d.LastPage > pages[d.File] {
			errs = append(errs, fmt.Errorf("%s: pages %d to %d aren't in %s", d.Document, d.FirstPage, d.LastPage, d.File))
		}
		for _, f := range d.Fields {
			if f.Page < d.FirstPage || f.Page > d.LastPage {
				errs = append(errs, fmt.Errorf("%s %s: page %d is outside the document", d.Document, f.Name, f.Page))
			}
			if f.Line == "" {
				continue
			}
			l, ok := lines[f.Line]
			if !ok {
				errs = append(errs, fmt.Errorf("%s %s: line %s isn't a facts line", d.Document, f.Name, f.Line))
				continue
			}
			want, checked := map[string]int64{"quantity": l.Quantity, "unit_price": int64(l.UnitPrice), "amount": int64(l.Amount)}[f.Name]
			if checked && f.Value != itoa(want) {
				errs = append(errs, fmt.Errorf("%s %s of line %s is %s, but its facts say %d", d.Document, f.Name, f.Line, f.Value, want))
			}
		}
	}
	return errs
}
