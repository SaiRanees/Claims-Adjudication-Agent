package rules_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

func goDigitPack(t *testing.T) *rules.Pack {
	t.Helper()
	p, err := rules.ParsePack([]byte(goDigitSource(t)))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// validClaim is a small valid claim: a ward stay moving to ICU, with a
// cataract line so every kind of field is set. Each call returns fresh
// slices, so a test can change them.
func validClaim(t *testing.T) (rules.Facts, rules.Cover) {
	t.Helper()
	f := rules.Facts{
		Admission: date(t, "2025-06-10"),
		Discharge: date(t, "2025-06-14"),
		Segments: []rules.Segment{
			{From: date(t, "2025-06-10"), To: date(t, "2025-06-11"), Kind: rules.SegmentRoom},
			{From: date(t, "2025-06-12"), To: date(t, "2025-06-14"), Kind: rules.SegmentICU},
		},
		DifferentialBilling: rules.AnswerYes,
		Lines: []rules.Line{
			{ID: "L03", Category: rules.CategoryPharmacyConsumables, ServiceDate: date(t, "2025-06-12"), Quantity: 1, UnitPrice: 3_000_000, Amount: 3_000_000},
			{ID: "L01", Category: rules.CategoryRoomRent, ServiceDate: date(t, "2025-06-10"), Quantity: 2, UnitPrice: 400_000, Amount: 800_000},
			{ID: "L02", Category: rules.CategoryICUCharges, ServiceDate: date(t, "2025-06-12"), Quantity: 2, UnitPrice: 900_000, Amount: 1_800_000},
			{ID: "L04", Category: rules.CategoryImplantsDevices, Quantity: 1, UnitPrice: 1_200_000, Amount: 1_200_000,
				Treatment: rules.Treatment{Kind: rules.TreatmentCataract, Eye: rules.EyeRight}},
			{ID: "L05", Category: rules.CategoryOtherHospitalCharges, ServiceDate: date(t, "2025-06-10"), Quantity: 1, UnitPrice: 200_000, Amount: 200_000, ItemList: "list-ii"},
		},
	}
	c := rules.Cover{
		Tranches: []rules.Tranche{{SumInsured: 50_000_000, ContinuousSince: date(t, "2023-04-01")}},
	}
	return f, c
}

func TestAdjudicateInputChecks(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, f *rules.Facts, c *rules.Cover)
		field  string
	}{
		{"no admission", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Admission = rules.Date{} }, "Facts.Admission"},
		{"discharge before admission", func(t *testing.T, f *rules.Facts, _ *rules.Cover) { f.Discharge = date(t, "2025-06-09") }, "Facts.Discharge"},
		{"no segments", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Segments = nil }, "Facts.Segments"},
		{"first segment starts late", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments[0].From = date(t, "2025-06-11")
		}, "Facts.Segments[0].From"},
		{"gap between segments", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments[1].From = date(t, "2025-06-13")
		}, "Facts.Segments[1].From"},
		{"overlapping segments", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments[1].From = date(t, "2025-06-11")
		}, "Facts.Segments[1].From"},
		{"segment ends before it starts", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments[0].To = date(t, "2025-06-09")
		}, "Facts.Segments[0].To"},
		{"segments end before discharge", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments[1].To = date(t, "2025-06-13")
		}, "Facts.Segments[1].To"},
		{"segment runs past discharge", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments[1].To = date(t, "2025-06-15")
		}, "Facts.Segments[1].To"},
		{"segment after discharge", func(t *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Segments = append(f.Segments, rules.Segment{From: date(t, "2025-06-15"), To: date(t, "2025-06-15"), Kind: rules.SegmentRoom})
		}, "Facts.Segments[2]"},
		{"unknown segment kind", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Segments[0].Kind = "ward" }, "Facts.Segments[0].Kind"},
		{"unknown differential billing", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.DifferentialBilling = "" }, "Facts.DifferentialBilling"},
		{"negative discount", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.BillDiscount = -1 }, "Facts.BillDiscount"},
		{"no lines", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines = nil }, "Facts.Lines"},
		{"line ID used twice", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[1].ID = "L03" }, "Facts.Lines[1].ID"},
		{"no line ID", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[0].ID = "" }, "Facts.Lines[0].ID"},
		{"unknown category", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[0].Category = "ward-charges" }, "Facts.Lines[0].Category"},
		{"zero quantity", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Lines[0].Quantity, f.Lines[0].UnitPrice, f.Lines[0].Amount = 0, 0, 0
		}, "Facts.Lines[0].Quantity"},
		{"negative unit price", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[0].UnitPrice = -1 }, "Facts.Lines[0].UnitPrice"},
		{"negative amount", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[0].Amount = -1 }, "Facts.Lines[0].Amount"},
		{"amount isn't quantity times price", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[1].Amount = 800_001 }, "Facts.Lines[1].Amount"},
		{"quantity times price overflows", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Lines[1].Quantity, f.Lines[1].UnitPrice = 4, 1<<62
		}, "Facts.Lines[1].Amount"},
		{"bill total overflows", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Lines[0].UnitPrice, f.Lines[0].Amount = 1<<62, 1<<62
			f.Lines[2].Quantity, f.Lines[2].UnitPrice, f.Lines[2].Amount = 1, 1<<62, 1<<62
		}, "Facts.Lines"},
		{"unknown item list", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[4].ItemList = "list-vi" }, "Facts.Lines[4].ItemList"},
		// A room or ICU line's date picks its segment, so in a stay with more
		// than one, it needs one.
		{"undated room line in a stay with two segments", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Lines[1].ServiceDate = rules.Date{}
		}, "Facts.Lines[1].ServiceDate"},
		{"unknown treatment", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[3].Treatment.Kind = "dental" }, "Facts.Lines[3].Treatment.Kind"},
		{"cataract with no eye", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Lines[3].Treatment.Eye = rules.EyeNone }, "Facts.Lines[3].Treatment.Eye"},
		{"eye on a line that isn't a cataract", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) {
			f.Lines[0].Treatment.Eye = rules.EyeLeft
		}, "Facts.Lines[0].Treatment.Eye"},
		{"unknown listed group", func(_ *testing.T, f *rules.Facts, _ *rules.Cover) { f.Condition.ListedGroup = "listed-12-months" }, "Facts.Condition.ListedGroup"},
		{"no tranches", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.Tranches = nil }, "Cover.Tranches"},
		{"zero sum insured", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.Tranches[0].SumInsured = 0 }, "Cover.Tranches[0].SumInsured"},
		{"tranche with no start", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.Tranches[0].ContinuousSince = rules.Date{} }, "Cover.Tranches[0].ContinuousSince"},
		{"negative bonus", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.CumulativeBonus = -1 }, "Cover.CumulativeBonus"},
		{"negative paid", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.PaidThisYear = -1 }, "Cover.PaidThisYear"},
		{"negative cataract use", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.CataractUsed.Left = -1 }, "Cover.CataractUsed.Left"},
		{"negative C.I.6 use", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) { c.ModernTreatmentUsed = -1 }, "Cover.ModernTreatmentUsed"},
		{"sum insured and bonus overflow", func(_ *testing.T, _ *rules.Facts, c *rules.Cover) {
			c.Tranches[0].SumInsured, c.CumulativeBonus = 1<<62, 1<<62
		}, "Cover"},
	}
	pack := goDigitPack(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, c := validClaim(t)
			tt.change(t, &f, &c)
			_, err := rules.Adjudicate(pack, f, c)
			if !errors.Is(err, rules.ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
			var ie *rules.InputError
			if !errors.As(err, &ie) || ie.Field != tt.field {
				t.Errorf("error names %v, want %s", err, tt.field)
			}
		})
	}
}

func TestAdjudicateValidInput(t *testing.T) {
	pack := goDigitPack(t)
	f, c := validClaim(t)
	// A same-day stay is one segment of one day, and an undated line is valid.
	f.Discharge = f.Admission
	f.Segments = []rules.Segment{{From: f.Admission, To: f.Admission, Kind: rules.SegmentRoom}}
	f.Lines[0].ServiceDate = rules.Date{}
	if _, err := rules.Adjudicate(pack, f, c); err != nil {
		t.Fatalf("same-day stay: %v", err)
	}
	if _, err := rules.Adjudicate(nil, f, c); !errors.Is(err, rules.ErrNoPackGiven) {
		t.Errorf("no pack: error = %v, want ErrNoPackGiven", err)
	}
}

// validClaim has one listed line and nothing over a cap or a sub-limit, so
// the listed line pays nothing and every other line pays 95% (the
// co-payment, 5% of ₹68,000, spreads exactly). Its undated implant is exempt
// from scaling, so it isn't flagged. Eligibility passes every tranche until
// session 5.
func TestAdjudicateValidClaim(t *testing.T) {
	pack := goDigitPack(t)
	f, c := validClaim(t)
	res, err := rules.Adjudicate(pack, f, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pack != pack.Identity() {
		t.Errorf("result pack = %s, want %s", res.Pack, pack.Identity())
	}
	var ids []string
	for _, l := range res.Lines {
		ids = append(ids, l.ID)
	}
	if !slices.Equal(ids, []string{"L01", "L02", "L03", "L04", "L05"}) {
		t.Errorf("lines in order %v, want sorted by ID", ids)
	}
	listed := rules.Adjustment{
		LineID: "L05", RuleID: "annexure-a-list-ii", Clause: "Annexure A List II",
		Inputs: []rules.Input{{Name: "amount", Value: 200_000}}, Amount: 200_000,
	}
	if len(res.Adjustments) == 0 || !reflect.DeepEqual(res.Adjustments[0], listed) {
		t.Errorf("first adjustment = %+v, want %+v", res.Adjustments, listed)
	}
	var got []wantAdj
	for _, a := range res.Adjustments {
		got = append(got, wantAdj{a.LineID, a.RuleID, a.Amount})
	}
	want := []wantAdj{
		{"L05", "annexure-a-list-ii", 200_000},
		{"L01", "co-payment", 40_000}, {"L02", "co-payment", 90_000},
		{"L03", "co-payment", 150_000}, {"L04", "co-payment", 60_000},
	}
	if !slices.Equal(got, want) {
		t.Errorf("adjustments\n got %v\nwant %v", got, want)
	}
	if len(res.Flags) != 0 {
		t.Errorf("flags %v, want none", res.Flags)
	}
	if res.Billed != 7_000_000 || res.Payable != 6_460_000 || res.Ledger.Paid != 6_460_000 {
		t.Errorf("billed %d, payable %d, paid %d; want 7,000,000, 6,460,000 and 6,460,000", res.Billed, res.Payable, res.Ledger.Paid)
	}
	// The cataract line's use is what it was admitted at before the
	// co-payment.
	if res.Ledger.CataractUsed != (rules.EyeAmounts{Right: 1_200_000}) {
		t.Errorf("cataract use = %+v", res.Ledger.CataractUsed)
	}
	checkLines(t, f, res)
}

// Every step but eligibility is coded; session 5 codes it.
func TestImplemented(t *testing.T) {
	for _, s := range rules.StepNames() {
		if got, want := rules.Implemented(s), s != rules.StepCheckEligibility; got != want {
			t.Errorf("Implemented(%s) = %t, want %t", s, got, want)
		}
	}
}

func TestAdjudicateLineOrder(t *testing.T) {
	pack := goDigitPack(t)
	f, c := validClaim(t)
	want, err := rules.Adjudicate(pack, f, c)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(f.Lines)
	got, err := rules.Adjudicate(pack, f, c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reversing the lines changed the result:\n got %+v\nwant %+v", got, want)
	}
}

// The flag list is closed, and what it says about each code is consistent.
func TestFlagCodes(t *testing.T) {
	codes := rules.FlagCodes()
	if len(codes) != 12 {
		t.Errorf("%d flag codes, want 12", len(codes))
	}
	lineFlags := []rules.FlagCode{
		rules.FlagRoomDaysMismatch, rules.FlagBucketSegmentMismatch, rules.FlagUndatedLine,
		rules.FlagOutsideStay, rules.FlagPackageRate,
	}
	withFigure := []rules.FlagCode{
		rules.FlagBillDiscount, rules.FlagICURatioScaling, rules.FlagCoverExceeded, rules.FlagTrancheExcluded,
	}
	for _, c := range codes {
		if got := c.AboutLine(); got != slices.Contains(lineFlags, c) {
			t.Errorf("%s.AboutLine() = %t", c, got)
		}
		if got := c.HasFigure(); got != slices.Contains(withFigure, c) {
			t.Errorf("%s.HasFigure() = %t", c, got)
		}
		if c.AboutLine() && c.HasFigure() {
			t.Errorf("%s is about a line and carries a figure", c)
		}
		if !slices.Contains(rules.StepNames(), c.Step()) {
			t.Errorf("%s.Step() = %q", c, c.Step())
		}
	}
}
