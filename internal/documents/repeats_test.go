package documents

import (
	"errors"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents/fixtures"
)

func TestRepeats(t *testing.T) {
	pg := func(file string, n int, fp string) PageFingerprint {
		return PageFingerprint{ID: PageID{File: file, Page: n}, Fingerprint: fp}
	}
	ref := func(file string, n int) *PageID { return &PageID{File: file, Page: n} }
	tests := []struct {
		name  string
		pages []PageFingerprint
		want  []*PageID
	}{
		{"none", nil, []*PageID{}},
		{"no repeats", []PageFingerprint{pg("a", 1, "x"), pg("a", 2, "y")}, []*PageID{nil, nil}},
		{
			"within a file, the later page repeats the first",
			[]PageFingerprint{pg("a", 1, "x"), pg("a", 2, "y"), pg("a", 3, "x")},
			[]*PageID{nil, nil, ref("a", 1)},
		},
		{
			"across files, every later copy names the first",
			[]PageFingerprint{pg("a", 1, "x"), pg("b", 1, "x"), pg("b", 2, "x")},
			[]*PageID{nil, ref("a", 1), ref("a", 1)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Repeats(tt.pages)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("%d results, want %d", len(got), len(tt.want))
			}
			for i := range got {
				switch {
				case (got[i] == nil) != (tt.want[i] == nil):
					t.Errorf("page %d: repeat of %v, want %v", i, got[i], tt.want[i])
				case got[i] != nil && *got[i] != *tt.want[i]:
					t.Errorf("page %d: repeat of %s, want %s", i, got[i], tt.want[i])
				}
			}
		})
	}
	if _, err := Repeats([]PageFingerprint{pg("a", 1, "")}); !errors.Is(err, ErrNoFingerprint) {
		t.Errorf("err = %v, want ErrNoFingerprint", err)
	}
}

// A page scanned twice into two files is found by its rendering, though the
// two files' bytes differ.
func TestRepeatsFromRendering(t *testing.T) {
	p := testPDFium(t)
	a := fixtures.LinesPDF("SYNTHETIC TEST DOCUMENT receipt R-1", "SYNTHETIC TEST DOCUMENT bill B-1")
	b := fixtures.LinesPDF("SYNTHETIC TEST DOCUMENT discharge summary", "SYNTHETIC TEST DOCUMENT bill B-1")
	var all []PageFingerprint
	for _, f := range []struct {
		name string
		data []byte
	}{{"a", a}, {"b", b}} {
		reads, err := p.ReadPages(t.Context(), f.data)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range reads {
			all = append(all, PageFingerprint{ID: PageID{File: f.name, Page: r.Number}, Fingerprint: r.Fingerprint})
		}
	}
	got, err := Repeats(all)
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range got {
		want := i == 3
		if (r != nil) != want {
			t.Errorf("%s: repeat of %v", all[i].ID, r)
		}
	}
	if got[3] == nil || *got[3] != (PageID{File: "a", Page: 2}) {
		t.Errorf("b#2 repeats %v, want a#2", got[3])
	}
}
