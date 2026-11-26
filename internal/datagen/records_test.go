package datagen

import (
	"os"
	"strings"
	"testing"
)

func surgerySource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/surgery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func surgerySpec(t *testing.T) Spec {
	t.Helper()
	s, err := ParseSpec("surgery", []byte(surgerySource(t)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRenderSurgery(t *testing.T) {
	p, err := Render(surgerySpec(t))
	if err != nil {
		t.Fatal(err)
	}
	tr := p.Truth
	// The claim form has two pages, then the photo ID; the discharge summary
	// two, then the OT notes; the bill one, then the invoice and receipt.
	type place struct {
		file        string
		first, last int
	}
	want := map[string]place{
		"CF": {"claim-form.pdf", 1, 2}, "ID": {"claim-form.pdf", 3, 3},
		"DS": {"hospital-records.pdf", 1, 2}, "OT": {"hospital-records.pdf", 3, 3},
		"IV": {"bills.pdf", 2, 2},
	}
	if len(tr.Other) != len(want) {
		t.Fatalf("%d other documents, want %d", len(tr.Other), len(want))
	}
	got := map[string]TruthField{}
	for _, d := range tr.Other {
		if w := want[d.Document]; d.File != w.file || d.FirstPage != w.first || d.LastPage != w.last {
			t.Errorf("%s is %s pages %d to %d, want %+v", d.Document, d.File, d.FirstPage, d.LastPage, w)
		}
		for _, f := range d.Fields {
			got[d.Document+"/"+f.Name] = f
		}
	}
	checks := map[string][2]string{
		"CF/claimed_amount": {"₹75,002.00", "7500200"},
		"CF/bill_amount":    {"₹75,002.00", "7500200"},
		"ID/date_of_birth":  {"14-Mar-1973", "1973-03-14"},
		"DS/onset":          {"20/08/2025", "2025-08-20"},
		"DS/segment_kind":   {"Ward", "room"},
		"DS/accident":       {"No", "false"},
		"OT/implant_serial": {"SYN-SN-M3X91K", "SYN-SN-M3X91K"},
		"IV/amount":         {"12,500.00", "1250000"},
		"IV/total":          {"Rs. 12,500.00", "1250000"},
	}
	for k, w := range checks {
		if f := got[k]; f.Printed != w[0] || f.Value != w[1] {
			t.Errorf("%s: printed %q value %q, want %q %q", k, f.Printed, f.Value, w[0], w[1])
		}
	}
	if f := got["IV/amount"]; f.Row != "L04" || f.Line != "L04" || f.Page != 2 {
		t.Errorf("IV/amount: %+v", f)
	}
	docs := strings.Join(asStrings(tr.Documents), ",")
	if docs != "claim-form,discharge-summary,hospital-bill,implant-invoice,ot-notes,payment-receipt,photo-id" {
		t.Errorf("documents: %s", docs)
	}
}

func TestSelfCheckSurgery(t *testing.T) {
	p, err := Render(surgerySpec(t))
	if err != nil {
		t.Fatal(err)
	}
	pdfium := newTestPDFium(t)
	if err := SelfCheck(t.Context(), pdfium, p); err != nil {
		t.Fatal(err)
	}
	// Each edit to one file's page text must be noticed.
	files := map[string][]byte{}
	for _, f := range p.Files {
		files[f.Name] = f.Data
	}
	tests := []struct {
		name, file string
		edit       func(pg []string)
		want       string
	}{
		{"a claimed amount wrong", "claim-form.pdf", func(pg []string) {
			// The one bill's net amount is the same, on the same page.
			pg[1] = strings.ReplaceAll(pg[1], "₹75,002.00", "₹75,020.00")
		}, "CF claimed_amount"},
		{"a claim form page marker missing", "claim-form.pdf", func(pg []string) { pg[1] = strings.ReplaceAll(pg[1], "Page 2 of 2", "") }, "CF page marker"},
		{"the ID's number missing", "claim-form.pdf", func(pg []string) { pg[2] = strings.ReplaceAll(pg[2], "SYN-ID-K7Q2-0461", "") }, "ID number"},
		{"a segment's dates on two lines", "hospital-records.pdf", func(pg []string) {
			pg[0] = strings.Replace(pg[0], "from 06/10/2025", "from\n06/10/2025", 1)
		}, "row segment-1"},
		{"a serial on another row", "hospital-records.pdf", func(pg []string) {
			pg[2] = strings.Replace(pg[2], "Sl. No. SYN-SN-M3X91K", "", 1) + "\nSYN-SN-M3X91K"
		}, "row L04"},
		{"the patient missing from page 2", "hospital-records.pdf", func(pg []string) {
			pg[1] = strings.ReplaceAll(pg[1], "Harish Nambiar", "")
		}, "DS patient"},
		{"an invoice description wrong", "bills.pdf", func(pg []string) {
			pg[1] = strings.Replace(pg[1], "Polypropylene Mesh", "Polypropylene Mash", 1)
		}, "IV description"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd := editedReader{PageReader: pdfium, file: files[tt.file], edit: tt.edit}
			err := SelfCheck(t.Context(), rd, p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestCheckTruthNoticesOtherDocuments(t *testing.T) {
	tests := []struct {
		name string
		edit func(d []TruthDocument)
		want string
	}{
		{"an invoice amount unlike the line's", func(d []TruthDocument) { setField(d, "IV", "amount", "1250001") }, "amount of line L04 is 1250001"},
		{"a field outside its document", func(d []TruthDocument) { d[0].Fields[0].Page = 3 }, "outside the document"},
		{"a document past its file's end", func(d []TruthDocument) { d[1].LastPage = 4 }, "aren't in claim-form.pdf"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Render(surgerySpec(t))
			if err != nil {
				t.Fatal(err)
			}
			tt.edit(p.Truth.Other)
			err = CheckTruth(p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func setField(docs []TruthDocument, doc, name, value string) {
	for i := range docs {
		for j := range docs[i].Fields {
			if docs[i].Document == doc && docs[i].Fields[j].Name == name {
				docs[i].Fields[j].Value = value
			}
		}
	}
}

// TestParseSpecRejectsRecords changes one thing in the surgery spec at a
// time; each must fail with an error naming it.
func TestParseSpecRejectsRecords(t *testing.T) {
	surgery := "  surgery:\n    date: 2025-10-07\n    surgeon: Dr. Rohit Bhalla\n    anaesthetist: Dr. Sunita Kher\n" +
		"    anaesthesia: Spinal\n    findings: A right indirect inguinal hernia with a 3 cm defect. The sac was reduced and a polypropylene mesh fixed.\n"
	course := "  course: >-\n    Admitted for elective repair. He underwent open mesh hernioplasty under\n" +
		"    spinal anaesthesia and was mobilised the next morning. Recovery was\n    uneventful.\n"
	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"an ID number in another shape", "number: SYN-ID-K7Q2-0461", "number: 1234 5678 9012", "isn't SYN-ID-XXXX-9999"},
		{"no date of birth", "  date_of_birth: 1973-03-14\n", "", "needs insured.date_of_birth"},
		{"a date of birth against the age", "date_of_birth: 1973-03-14", "date_of_birth: 1973-10-07", "gives age 51"},
		{"OT notes without surgery", surgery, "", "OT notes need clinical.surgery"},
		{"a surgery outside the stay", "    date: 2025-10-07\n", "    date: 2025-10-09\n", "outside the stay"},
		{"a discharge summary without a course", course, "", "needs clinical.course"},
		{"a claim form dated another day", "number: SYN-CF-000002, date: 2025-10-10", "number: SYN-CF-000002, date: 2025-10-11", "claim_form.signed_on"},
		{"a second claim form", "  - {id: R1,", "  - {id: CF2, type: claim-form, number: X, date: 2025-10-10}\n  - {id: R1,", "at most one claim-form"},
		{"an invoice for a line that isn't an implant", "line: L04, serial", "line: L03, serial", "isn't an implants-devices line"},
		{"a serial in another shape", "serial: SYN-SN-M3X91K", "serial: 12345", "isn't SYN-SN-XXXXXX"},
		{"an invoice without a vendor", "    vendor: {name: Kesarvan Surgical Supplies, address: \"5 Mill Road, Ravikund\", registration: SYN-VR-0002}\n", "", "needs a vendor"},
		{"implants on a bill", "lines_per_page: 12}", "lines_per_page: 12, implants: [{line: L04, serial: SYN-SN-M3X91K}]}", "only an implant invoice"},
		{"a layout on a record", "type: ot-notes,", "type: ot-notes, layout: ledger,", "has no layout"},
	}
	src := surgerySource(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(src, tt.old) {
				t.Fatalf("the surgery spec has no %q", tt.old)
			}
			_, err := ParseSpec("surgery", []byte(strings.Replace(src, tt.old, tt.new, 1)))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
