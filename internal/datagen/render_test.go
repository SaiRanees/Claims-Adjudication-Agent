package datagen

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

func TestFormatAmount(t *testing.T) {
	tests := []struct {
		paise   int64
		indian  string
		western string
	}{
		{0, "0.00", "0.00"},
		{5, "0.05", "0.05"},
		{99900, "999.00", "999.00"},
		{100000, "1,000.00", "1,000.00"},
		{12345600, "1,23,456.00", "123,456.00"},
		{12345678, "1,23,456.78", "123,456.78"},
		{1234567890, "1,23,45,678.90", "12,345,678.90"},
		{9223372036854775807, "92,23,37,20,36,85,47,758.07", "92,233,720,368,547,758.07"},
	}
	for _, tt := range tests {
		if got := FormatAmount(tt.paise, GroupIndian); got != tt.indian {
			t.Errorf("Indian %d: %q, want %q", tt.paise, got, tt.indian)
		}
		if got := FormatAmount(tt.paise, GroupWestern); got != tt.western {
			t.Errorf("Western %d: %q, want %q", tt.paise, got, tt.western)
		}
	}
}

func TestFormatDate(t *testing.T) {
	d, err := rules.NewDate(2025, time.September, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatDate(d, DateSlashes); got != "02/09/2025" {
		t.Errorf("slashes: %q", got)
	}
	if got := FormatDate(d, DateMonthName); got != "02-Sep-2025" {
		t.Errorf("month name: %q", got)
	}
}

// TestLayoutsDiffer checks the plan's requirement: the three hospital-bill
// layouts differ in column order, date style, grouping and mark, and every
// layout shows every field once, with columns in order and apart.
func TestLayoutsDiffer(t *testing.T) {
	orders := map[string]bool{}
	dates := map[DateStyle]bool{}
	groupings := map[Grouping]bool{}
	marks := map[string]bool{}
	all := map[string]Style{"pharmacy": pharmacyStyle}
	for _, l := range Layouts() {
		st := styles[l]
		all[string(l)] = st
		var order []string
		for _, c := range st.Columns {
			order = append(order, string(c.Field))
		}
		orders[strings.Join(order, ",")] = true
		dates[st.Dates] = true
		groupings[st.Grouping] = true
		marks[st.Mark] = true
	}
	if len(orders) != 3 || len(dates) != 2 || len(groupings) != 2 || len(marks) != 3 {
		t.Errorf("layouts don't differ enough: %d orders, %d date styles, %d groupings, %d marks", len(orders), len(dates), len(groupings), len(marks))
	}
	for name, st := range all {
		seen := map[ColumnField]bool{}
		right := marginLeft
		for _, c := range st.Columns {
			if seen[c.Field] {
				t.Errorf("%s: %s twice", name, c.Field)
			}
			seen[c.Field] = true
			if c.X < right || c.X+c.Width > marginRight+0.01 {
				t.Errorf("%s: column %s overlaps or runs past the margin", name, c.Field)
			}
			right = c.X + c.Width
		}
		for _, f := range []ColumnField{FieldDate, FieldDescription, FieldQuantity, FieldUnitPrice, FieldAmount} {
			if !seen[f] {
				t.Errorf("%s: no %s column", name, f)
			}
		}
	}
}

func TestRenderExample(t *testing.T) {
	s := exampleSpec(t)
	p, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 || p.Files[0].Name != "hospital-bill.pdf" {
		t.Fatalf("files: %v", len(p.Files))
	}
	tr := p.Truth
	// B1 holds five lines at three a page, so two pages; R1 is the third.
	wantParts := []TruthPart{
		{Document: "B1", Type: DocHospitalBill, FirstPage: 1, LastPage: 2},
		{Document: "R1", Type: DocPaymentReceipt, FirstPage: 3, LastPage: 3},
	}
	if got := tr.Files[0].Parts; len(got) != 2 || got[0] != wantParts[0] || got[1] != wantParts[1] {
		t.Errorf("parts: %+v", got)
	}
	if len(tr.Files[0].Pages) != 3 || tr.Files[0].Pages[1].Type != DocHospitalBill {
		t.Errorf("pages: %+v", tr.Files[0].Pages)
	}
	b1 := tr.Bills[0]
	if b1.Totals.Page != 2 || b1.Totals.Gross.Printed != "₹1,43,607.00" || b1.Totals.Net.Paise != 14210700 {
		t.Errorf("totals: %+v", b1.Totals)
	}
	if b1.Lines[3].ServiceDate != nil || b1.Lines[3].Page != 2 || b1.Lines[2].Amount.Printed != "1,23,456.00" {
		t.Errorf("lines: %+v", b1.Lines)
	}
	if r := tr.Receipts[0]; r.Amount.Paise != 14210700+42500 || r.Page != 3 {
		t.Errorf("receipt: %+v", r)
	}
	if strings.Join(asStrings(tr.Documents), ",") != "hospital-bill,payment-receipt,pharmacy-bill" {
		t.Errorf("documents: %v", tr.Documents)
	}
	again, err := Render(s)
	if err != nil {
		t.Fatal(err)
	}
	for i := range p.Files {
		if !bytes.Equal(p.Files[i].Data, again.Files[i].Data) {
			t.Errorf("%s differs between renderings", p.Files[i].Name)
		}
	}
}

func asStrings(ts []DocumentType) []string {
	var out []string
	for _, t := range ts {
		out = append(out, string(t))
	}
	return out
}

func TestRenderRejectsAValueTooWide(t *testing.T) {
	src := strings.Replace(exampleSource(t), "description: Nursing Charges}",
		"description: Nursing Charges for the general ward including the night shift and the weekend}", 1)
	s, err := ParseSpec("example", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Render(s); !errors.Is(err, errTooWide) {
		t.Errorf("got %v, want errTooWide", err)
	}
}
