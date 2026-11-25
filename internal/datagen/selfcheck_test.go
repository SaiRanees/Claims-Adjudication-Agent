package datagen

import (
	"context"
	"strings"
	"testing"
)

func TestSelfCheckExample(t *testing.T) {
	p, err := Render(exampleSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := SelfCheck(t.Context(), newTestPDFium(t), p); err != nil {
		t.Fatal(err)
	}
}

// editedReader reads pages with PDFium, then edits one file's page text,
// so a test can show the check notices a value that isn't printed.
type editedReader struct {
	PageReader
	file []byte
	edit func(pages []string)
}

func (e editedReader) PageTexts(ctx context.Context, data []byte) ([]string, error) {
	pages, err := e.PageReader.PageTexts(ctx, data)
	if err == nil && string(data) == string(e.file) {
		e.edit(pages)
	}
	return pages, err
}

func TestSelfCheckNotices(t *testing.T) {
	p, err := Render(exampleSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	pdfium := newTestPDFium(t)
	hospital := p.Files[0].Data
	tests := []struct {
		name string
		edit func(pages []string)
		want string
	}{
		{"an amount wrong", func(pg []string) { pg[0] = strings.Replace(pg[0], "13,500.00", "13,050.00", 1) }, "line L01"},
		{"a description wrong", func(pg []string) { pg[0] = strings.Replace(pg[0], "Nursing Charges", "Nursing Charge", 1) }, "line L02"},
		{"a date on another row", func(pg []string) {
			// Move L01's date onto a line of its own: still on the page, and
			// on L02's row and in the header, but not on L01's row.
			pg[0] = strings.Replace(pg[0], "22/09/2025 Room Rent", "Room Rent", 1) + "\n22/09/2025"
		}, "line L01"},
		{"a total missing", func(pg []string) { pg[1] = strings.ReplaceAll(pg[1], "₹1,42,107.00", "") }, "B1 net"},
		{"a footer missing", func(pg []string) { pg[2] = strings.ReplaceAll(pg[2], Footer, "") }, "page 3: no footer"},
		{"a page marker missing", func(pg []string) { pg[1] = strings.ReplaceAll(pg[1], "Page 2 of 2", "") }, "page marker"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rd := editedReader{PageReader: pdfium, file: hospital, edit: tt.edit}
			err := SelfCheck(t.Context(), rd, p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestCheckTruthNotices(t *testing.T) {
	tests := []struct {
		name string
		edit func(p *Packet)
		want string
	}{
		{"a line printed with another amount", func(p *Packet) { p.Truth.Bills[0].Lines[0].Amount.Paise++ }, "line L01 is printed as"},
		{"a line printed twice", func(p *Packet) {
			p.Truth.Bills[1].Lines = append(p.Truth.Bills[1].Lines, p.Truth.Bills[0].Lines[0])
		}, "L01 is printed 2 times"},
		{"totals that don't add up", func(p *Packet) { p.Truth.Bills[0].Totals.Net.Paise-- }, "don't add up"},
		{"a file's bytes changed", func(p *Packet) { p.Files[1].Data = append(p.Files[1].Data, ' ') }, "file 1"},
		{"a part with a gap", func(p *Packet) { p.Truth.Files[0].Parts[1].FirstPage = 4 }, "doesn't start at page 3"},
		{"another generator version", func(p *Packet) { p.Truth.GeneratorVersion = "0" }, "generator version"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Render(exampleSpec(t))
			if err != nil {
				t.Fatal(err)
			}
			if err := CheckTruth(p); err != nil {
				t.Fatalf("before the edit: %v", err)
			}
			tt.edit(&p)
			err = CheckTruth(p)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}
