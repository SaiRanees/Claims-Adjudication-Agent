package extraction

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
)

// pg is a classified page: file, page, type, document number and marker,
// "" for null.
func pg(file string, n int, typ PageType, number, marker string) ClassifiedPage {
	p := ClassifiedPage{ID: documents.PageID{File: file, Page: n}, Type: typ}
	if number != "" {
		p.DocumentNumber = &number
	}
	if marker != "" {
		p.PageMarker = &marker
	}
	return p
}

// part is a wanted part, "" for no number.
func part(file string, typ PageType, first, last int, number string, relabelled ...int) Part {
	p := Part{File: file, Type: typ, FirstPage: first, LastPage: last, Relabelled: relabelled}
	if number != "" {
		p.DocumentNumber = &number
	}
	return p
}

func TestGroup(t *testing.T) {
	const (
		bill    = PageHospitalBill
		pharm   = PagePharmacyBill
		receipt = PagePaymentReceipt
		form    = PageClaimForm
		ds      = PageDischargeSummary
		id      = PagePhotoID
	)
	tests := []struct {
		name  string
		pages []ClassifiedPage
		want  []Part
	}{
		{
			name: "a bill over three pages",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", "Page 1 of 3"), pg("b", 2, bill, "IP-1", "Page 2 of 3"), pg("b", 3, bill, "IP-1", "Page 3 of 3"),
			},
			want: []Part{part("b", bill, 1, 3, "IP-1")},
		},
		{
			name: "a bill split by one misread page in the middle, which prints the bill's number",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", "Page 1 of 3"), pg("b", 2, pharm, "IP-1", "Page 2 of 3"), pg("b", 3, bill, "IP-1", "Page 3 of 3"),
			},
			want: []Part{part("b", bill, 1, 3, "IP-1", 2)},
		},
		{
			name: "a misread middle page with its number misread too, joined by its marker",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", "Page 1 of 3"), pg("b", 2, other(), "", "Page 2 of 3"), pg("b", 3, bill, "IP-1", "Page 3 of 3"),
			},
			want: []Part{part("b", bill, 1, 3, "IP-1", 2)},
		},
		{
			name: "a misread last page",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", "Page 1 of 2"), pg("b", 2, receipt, "IP-1", "Page 2 of 2"),
			},
			want: []Part{part("b", bill, 1, 2, "IP-1", 2)},
		},
		{
			name: "a misread first page",
			pages: []ClassifiedPage{
				pg("b", 1, pharm, "", "Page 1 of 2"), pg("b", 2, bill, "IP-1", "Page 2 of 2"),
			},
			want: []Part{part("b", bill, 1, 2, "IP-1", 1)},
		},
		{
			name: "a page of another type with nothing tying it to the bill stays its own part",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", "Page 1 of 2"), pg("b", 2, receipt, "RC-9", ""), pg("b", 3, bill, "IP-1", "Page 2 of 2"),
			},
			want: []Part{part("b", bill, 1, 1, "IP-1"), part("b", receipt, 2, 2, "RC-9"), part("b", bill, 3, 3, "IP-1")},
		},
		{
			name: "two bills back to back, told apart by their numbers",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", ""), pg("b", 2, bill, "IP-1", ""), pg("b", 3, bill, "IP-2", ""), pg("b", 4, bill, "IP-2", ""),
			},
			want: []Part{part("b", bill, 1, 2, "IP-1"), part("b", bill, 3, 4, "IP-2")},
		},
		{
			name: "two bills back to back, the second's number unread, told apart by its page 1",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "IP-1", "Page 1 of 1"), pg("b", 2, bill, "", "Page 1 of 2"), pg("b", 3, bill, "IP-2", "Page 2 of 2"),
			},
			want: []Part{part("b", bill, 1, 1, "IP-1"), part("b", bill, 2, 3, "IP-2")},
		},
		{
			name: "numbers compared without case or spaces, and an absent one joins",
			pages: []ClassifiedPage{
				pg("b", 1, bill, "SYN-IP 0001", ""), pg("b", 2, bill, "", ""), pg("b", 3, bill, "syn-ip0001", ""),
			},
			want: []Part{part("b", bill, 1, 3, "SYN-IP 0001")},
		},
		{
			name: "one file holding several documents",
			pages: []ClassifiedPage{
				pg("all", 1, form, "CF-1", "Page 1 of 2"), pg("all", 2, form, "CF-1", "Page 2 of 2"),
				pg("all", 3, id, "SYN-ID-AAAA-0001", ""),
				pg("all", 4, ds, "DS-1", "Page 1 of 2"), pg("all", 5, ds, "DS-1", "Page 2 of 2"),
				pg("all", 6, bill, "IP-1", "Page 1 of 2"), pg("all", 7, bill, "IP-1", "Page 2 of 2"),
				pg("all", 8, pharm, "PH-1", "Page 1 of 1"),
				pg("all", 9, receipt, "RC-1", ""),
			},
			want: []Part{
				part("all", form, 1, 2, "CF-1"), part("all", id, 3, 3, "SYN-ID-AAAA-0001"), part("all", ds, 4, 5, "DS-1"),
				part("all", bill, 6, 7, "IP-1"), part("all", pharm, 8, 8, "PH-1"), part("all", receipt, 9, 9, "RC-1"),
			},
		},
		{
			name: "a part never crosses files",
			pages: []ClassifiedPage{
				pg("a", 1, bill, "IP-1", "Page 1 of 2"), pg("b", 1, bill, "IP-1", "Page 2 of 2"),
			},
			want: []Part{part("a", bill, 1, 1, "IP-1"), part("b", bill, 1, 1, "IP-1")},
		},
		{name: "no pages", pages: nil, want: []Part{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Group(tt.pages)
			if err != nil {
				t.Fatal(err)
			}
			gj, _ := json.Marshal(got)
			wj, _ := json.Marshal(tt.want)
			if string(gj) != string(wj) {
				t.Errorf("got  %s\nwant %s", gj, wj)
			}
		})
	}
}

func other() PageType { return PageOther }

func TestGroupNeedsPagesInOrder(t *testing.T) {
	for _, pages := range [][]ClassifiedPage{
		{pg("a", 2, PageOther, "", ""), pg("a", 1, PageOther, "", "")},
		{pg("a", 1, PageOther, "", ""), pg("a", 3, PageOther, "", "")},
	} {
		if _, err := Group(pages); err == nil {
			t.Errorf("pages %v grouped", []documents.PageID{pages[0].ID, pages[1].ID})
		}
	}
}

func TestParseMarker(t *testing.T) {
	tests := []struct {
		in     string
		x, of  int
		parsed bool
	}{
		{"Page 2 of 3", 2, 3, true},
		{"page 2/3", 2, 3, true},
		{"Pg. 1 of 1", 1, 1, true},
		{"PAGE 10 OF 12", 10, 12, true},
		{"Page 4 of 3", 0, 0, false},
		{"Page 0 of 3", 0, 0, false},
		{"2 of 3", 0, 0, false},
		{"Pages", 0, 0, false},
	}
	for _, tt := range tests {
		m, ok := parseMarker(&tt.in)
		if ok != tt.parsed || m.x != tt.x || m.of != tt.of {
			t.Errorf("parseMarker(%q) = %+v, %v", tt.in, m, ok)
		}
	}
	if _, ok := parseMarker(nil); ok {
		t.Error("nil parsed")
	}
}

func TestChecklist(t *testing.T) {
	base := []PageType{PageClaimForm, PageDischargeSummary, PageHospitalBill, PagePaymentReceipt}
	tests := []struct {
		name     string
		present  []PageType
		claim    ClaimContext
		required []PageType
		missing  []PageType
	}{
		{"a complete medical packet", base, ClaimContext{ClaimedPaise: 50_000_00}, base, nil},
		{"photo ID and pharmacy bill aren't required", []PageType{PageClaimForm, PagePhotoID, PagePharmacyBill},
			ClaimContext{}, base, []PageType{PageDischargeSummary, PageHospitalBill, PagePaymentReceipt}},
		{"surgery needs OT notes", base, ClaimContext{Surgery: true},
			append(slices.Clone(base), PageOTNotes), []PageType{PageOTNotes}},
		{"a billed implant needs its invoice", append(slices.Clone(base), PageImplantInvoice), ClaimContext{ImplantBilled: true},
			append(slices.Clone(base), PageImplantInvoice), nil},
		{"exactly ₹1 lakh needs no KYC", base, ClaimContext{ClaimedPaise: KYCThresholdPaise}, base, nil},
		{"over ₹1 lakh needs KYC", base, ClaimContext{ClaimedPaise: KYCThresholdPaise + 1},
			append(slices.Clone(base), PageKYC), []PageType{PageKYC}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := Checklist(tt.present, tt.claim)
			var req []PageType
			for _, it := range items {
				req = append(req, it.Type)
			}
			if !slices.Equal(req, tt.required) {
				t.Errorf("required %v, want %v", req, tt.required)
			}
			if got := Missing(items); !slices.Equal(got, tt.missing) {
				t.Errorf("missing %v, want %v", got, tt.missing)
			}
		})
	}
	if KYCThresholdPaise != 10_000_000 {
		t.Errorf("KYC threshold %d paise, want ₹1,00,000.00", KYCThresholdPaise)
	}
}

func TestPartTypes(t *testing.T) {
	parts := []Part{{Type: PageHospitalBill}, {Type: PagePharmacyBill}, {Type: PageHospitalBill}}
	if got := PartTypes(parts); !slices.Equal(got, []PageType{PageHospitalBill, PagePharmacyBill}) {
		t.Errorf("got %v", got)
	}
}
