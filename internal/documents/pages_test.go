package documents

import (
	"errors"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents/fixtures"
)

func TestHasTextLayer(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"white space only", " \n\t\r  ", false},
		{"one short of the threshold", strings.Repeat("x", TextLayerMinChars-1), false},
		{"exactly the threshold", strings.Repeat("x", TextLayerMinChars), true},
		{"spaces don't count", strings.Repeat("x ", TextLayerMinChars-1), false},
		{"newlines don't count", strings.Repeat("x\r\n", TextLayerMinChars), true},
		{"non-ASCII counts per character", strings.Repeat("₹", TextLayerMinChars), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasTextLayer(tt.text); got != tt.want {
				t.Errorf("HasTextLayer(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
	if TextLayerMinChars != 20 {
		t.Errorf("TextLayerMinChars = %d; week-3.md sets it at 20", TextLayerMinChars)
	}
}

func TestPrepare(t *testing.T) {
	p := testPDFium(t)
	data := fixtures.LinesPDF(
		"SYNTHETIC TEST DOCUMENT bill 1 page 1 of 2",
		"SYNTHETIC TEST DOCUMENT bill 1 page 2 of 2",
		"short",
		"",
		"SYNTHETIC TEST DOCUMENT bill 1 page 1 of 2",
	)
	pages, err := Prepare(t.Context(), p, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 5 {
		t.Fatalf("%d pages, want 5", len(pages))
	}
	wantText := []bool{true, true, false, false, true}
	for i, pg := range pages {
		if pg.Number != i+1 {
			t.Errorf("page %d numbered %d", i+1, pg.Number)
		}
		if pg.HasTextLayer != wantText[i] {
			t.Errorf("page %d: text layer %v, want %v (text %q)", i+1, pg.HasTextLayer, wantText[i], pg.Text)
		}
		if len(pg.Fingerprint) != 64 {
			t.Errorf("page %d: fingerprint %q", i+1, pg.Fingerprint)
		}
		// Each split page is a PDF of one page that PDFium reads back with
		// the same text and the same rendering.
		n, err := p.PageCount(t.Context(), pg.PDF, 500)
		if err != nil || n != 1 {
			t.Fatalf("page %d's split PDF: %d pages, %v", i+1, n, err)
		}
		back, err := p.ReadPages(t.Context(), pg.PDF)
		if err != nil {
			t.Fatal(err)
		}
		if back[0].Text != pg.Text || back[0].Fingerprint != pg.Fingerprint {
			t.Errorf("page %d's split PDF reads back as %q, fingerprint %s; want %q, %s",
				i+1, back[0].Text, back[0].Fingerprint, pg.Text, pg.Fingerprint)
		}
	}
	// The text comes with its position: one span, at the line's place.
	first := pages[0]
	if len(first.Spans) != 1 || first.Spans[0].Text != "SYNTHETIC TEST DOCUMENT bill 1 page 1 of 2" {
		t.Fatalf("page 1's spans: %+v", first.Spans)
	}
	s := first.Spans[0]
	if s.Left < 71 || s.Left > 73 || s.Right <= s.Left || s.Top <= s.Bottom {
		t.Errorf("page 1's span box is %+v; the line starts 72 points in", s)
	}
	// Same rendering, same fingerprint; different text, different one; and
	// a page with no text still has one.
	if pages[0].Fingerprint != pages[4].Fingerprint {
		t.Error("two identical pages have different fingerprints")
	}
	if pages[0].Fingerprint == pages[1].Fingerprint || pages[2].Fingerprint == pages[3].Fingerprint {
		t.Error("different pages share a fingerprint")
	}
}

// TestPageFingerprintPinned pins one page's fingerprint, which CI checks on
// both its Linux and its Windows job: PDFium runs as the same WebAssembly
// module on both, so its rendering must be the same pixel for pixel. A
// difference between the jobs is a bug to find, never a pin to move.
func TestPageFingerprintPinned(t *testing.T) {
	const pinned = "0a5c8ef979090dc2bf95ac3a0076abfaf382fe2c05e8d1ec3141c155724b8fa1"
	reads, err := testPDFium(t).ReadPages(t.Context(), fixtures.LinesPDF("SYNTHETIC TEST DOCUMENT fingerprint pin"))
	if err != nil {
		t.Fatal(err)
	}
	if reads[0].Fingerprint != pinned {
		t.Errorf("fingerprint %s, pinned %s", reads[0].Fingerprint, pinned)
	}
}

func TestPrepareRejects(t *testing.T) {
	p := testPDFium(t)
	ten := fixtures.TextPDF(10)
	for _, tt := range []struct {
		name string
		data []byte
		err  error
	}{
		{"truncated", ten[:len(ten)/3], ErrUnreadable},
		{"encrypted", fixtures.EncryptedPDF(t, ""), ErrEncrypted},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Prepare(t.Context(), p, tt.data); !errors.Is(err, tt.err) {
				t.Errorf("err = %v, want %v", err, tt.err)
			}
		})
	}
}

func TestSplitPagesDeadline(t *testing.T) {
	if _, err := SplitPages(t.Context(), fixtures.TextPDF(3), 0); !errors.Is(err, ErrUnreadable) {
		t.Errorf("err = %v, want ErrUnreadable at a spent deadline", err)
	}
}
