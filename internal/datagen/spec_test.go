package datagen

import (
	"os"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

func exampleSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exampleSpec(t *testing.T) Spec {
	t.Helper()
	s, err := ParseSpec("example", []byte(exampleSource(t)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseSpecExample(t *testing.T) {
	s := exampleSpec(t)
	period, cover, facts, err := s.Engine()
	if err != nil {
		t.Fatal(err)
	}
	if period.WordingUIN != "GODHLIP24179V022324" || len(cover.Tranches) != 1 || cover.Tranches[0].SumInsured != 50000000 {
		t.Errorf("policy or cover: %+v %+v", period, cover)
	}
	if len(facts.Lines) != 6 || facts.BillDiscount != 150000 || facts.DifferentialBilling != rules.AnswerYes {
		t.Fatalf("facts: %+v", facts)
	}
	if !facts.Lines[3].ServiceDate.IsZero() {
		t.Error("an undated line got a date")
	}
	if facts.Lines[4].ItemList != "list-ii" || facts.Lines[2].Amount != money.Paise(12345600) {
		t.Errorf("lines: %+v", facts.Lines)
	}
}

// TestParseSpecRejects changes one thing in the example at a time; each must
// fail with an error naming it.
func TestParseSpecRejects(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"unknown field", "  city: Amberpur\n", "  city: Amberpur\n  pin: 000000\n", "field pin not found"},
		{"second document", "", "---\n{}\n", "more than one document"},
		{"bad date", "admission: 2025-09-22", "admission: 2025-09-31", "facts.admission"},
		{"empty name", "name: Meera Pillai", "name: \"\"", "insured.name is empty"},
		{"unknown layout", "layout: ledger", "layout: fancy", "layout \"fancy\""},
		{"discounts disagree", "discount_paise: 150000}", "discount_paise: 100000}", "add up to 100000"},
		{"receipt pays a receipt", "against: [B1, P1]", "against: [B1, R1]", "isn't a bill"},
		{"document in no file", "documents: [P1]}", "documents: [B1]}", "already in a file"},
		{"line without a format", "  - {line: L06, bill: P1, description: Inj. Ceftriaxone 1 g}\n", "", "L06 has no entry"},
		{"format without a line", "  - {line: L06, bill: P1, description: Inj. Ceftriaxone 1 g}\n",
			"  - {line: L06, bill: P1, description: Inj. Ceftriaxone 1 g}\n  - {line: L07, bill: P1, description: X}\n", "L07 isn't a facts line"},
		{"pharmacy bill lists a room", "{line: L06, bill: P1", "{line: L06, bill: B1", "P1 has no lines"},
		{"room on a pharmacy bill", "{line: L01, bill: B1", "{line: L01, bill: P1", "only pharmacy-consumables"},
		{"listed line without an item", ", annexure: list-ii/27}", "}", "needs an annexure item"},
		{"item on the wrong list", "annexure: list-ii/27", "annexure: list-i/11", "is on list-i"},
		{"description not the wording's", "description: ADMISSION KIT", "description: Admission Kit", "isn't the wording's"},
		{"unknown item", "annexure: list-ii/27", "annexure: list-ii/99", "isn't in the generator's table"},
		{"zero lines per page", "lines_per_page: 3", "lines_per_page: 0", "lines_per_page 0"},
		{"bad file name", "name: pharmacy-bill.pdf", "name: Pharmacy Bill.pdf", "name \"Pharmacy Bill.pdf\""},
	}
	src := exampleSource(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var changed string
			if tt.old == "" {
				changed = src + tt.new
			} else {
				if !strings.Contains(src, tt.old) {
					t.Fatalf("the example has no %q", tt.old)
				}
				changed = strings.Replace(src, tt.old, tt.new, 1)
			}
			_, err := ParseSpec("example", []byte(changed))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestAnnexureTable(t *testing.T) {
	seen := map[string]bool{}
	lists := map[string]bool{}
	for _, a := range AnnexureItems() {
		if seen[a.Key()] {
			t.Errorf("%s repeats", a.Key())
		}
		seen[a.Key()] = true
		lists[a.List] = true
		if a.Name == "" || a.Number < 1 || a.Page < 20 || a.Page > 24 {
			t.Errorf("%+v is incomplete or outside Annexure A's pages 20 to 24", a)
		}
	}
	for _, l := range []string{"list-i", "list-ii", "list-iii", "list-iv", "list-v"} {
		if !lists[l] {
			t.Errorf("no item from %s", l)
		}
	}
}
