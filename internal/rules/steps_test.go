package rules_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

// The step tests check what steps 1 to 3 (dropping listed items, setting
// aside unmodelled lines, the daily caps and proportionate deduction) do to
// claims the golden cases don't cover. They compare only those steps'
// adjustments and flags, so they hold as later sessions code the rest.

var steps1to3 = []rules.StepName{
	rules.StepDropListedItems, rules.StepSetAsideUnmodelledLines,
	rules.StepApplyDailyCaps, rules.StepApplyProportionateDeduction,
}

type wantAdj struct {
	line, rule string
	paise      money.Paise
}

func (a wantAdj) String() string { return fmt.Sprintf("%s %s %d", a.line, a.rule, a.paise) }

type wantFlag struct {
	code rules.FlagCode
	line string
	// figure is compared only for the codes that carry one.
	figure money.Paise
}

func (f wantFlag) String() string { return fmt.Sprintf("%s %s %d", f.code, f.line, f.figure) }

func ln(t *testing.T, id string, cat rules.Category, day string, qty int64, price money.Paise) rules.Line {
	t.Helper()
	l := rules.Line{ID: id, Category: cat, Quantity: qty, UnitPrice: price, Amount: money.Paise(qty) * price}
	if day != "" {
		l.ServiceDate = date(t, day)
	}
	return l
}

func seg(t *testing.T, from, to string, kind rules.SegmentKind) rules.Segment {
	t.Helper()
	return rules.Segment{From: date(t, from), To: date(t, to), Kind: kind}
}

// facts builds a claim whose stay runs from the first segment's start to the
// last segment's end.
func facts(billing rules.Answer, segs []rules.Segment, lines ...rules.Line) rules.Facts {
	return rules.Facts{
		Admission:           segs[0].From,
		Discharge:           segs[len(segs)-1].To,
		Segments:            segs,
		DifferentialBilling: billing,
		Lines:               lines,
	}
}

func coverOf(t *testing.T, sumInsured money.Paise) rules.Cover {
	t.Helper()
	return rules.Cover{Tranches: []rules.Tranche{{SumInsured: sumInsured, ContinuousSince: date(t, "2023-04-01")}}}
}

// early returns the adjustments and flags that steps 1 to 3 produce, in the
// result's order.
func early(t *testing.T, pack *rules.Pack, res rules.Result) ([]wantAdj, []wantFlag) {
	t.Helper()
	stepOf := map[string]rules.StepName{}
	for _, s := range pack.Steps {
		for _, r := range s.Rules {
			stepOf[r.ID] = s.Name
		}
	}
	var adjs []wantAdj
	for _, a := range res.Adjustments {
		if slices.Contains(steps1to3, stepOf[a.RuleID]) {
			adjs = append(adjs, wantAdj{a.LineID, a.RuleID, a.Amount})
		}
	}
	var flags []wantFlag
	for _, f := range res.Flags {
		if slices.Contains(steps1to3, f.Code.Step()) {
			flags = append(flags, wantFlag{f.Code, f.LineID, f.Figure})
		}
	}
	return adjs, flags
}

// checkLines checks that every line's billed less payable is its
// adjustments, and that the lines add up to the claim.
func checkLines(t *testing.T, f rules.Facts, res rules.Result) {
	t.Helper()
	adjusted := map[string]money.Paise{}
	for _, a := range res.Adjustments {
		if a.Amount <= 0 {
			t.Errorf("adjustment %s %s is %d; only positive adjustments are listed", a.LineID, a.RuleID, a.Amount)
		}
		adjusted[a.LineID] += a.Amount
	}
	var billed, payable money.Paise
	for _, l := range res.Lines {
		if l.Billed-l.Payable != adjusted[l.ID] {
			t.Errorf("%s: billed %d less payable %d isn't its adjustments, %d", l.ID, l.Billed, l.Payable, adjusted[l.ID])
		}
		billed += l.Billed
		payable += l.Payable
	}
	if len(res.Lines) != len(f.Lines) || billed != res.Billed || payable != res.Payable {
		t.Errorf("%d lines add to billed %d and payable %d; the result says %d and %d",
			len(res.Lines), billed, payable, res.Billed, res.Payable)
	}
}

func TestStepsOneToThree(t *testing.T) {
	goDigit := goDigitPack(t)
	icuNoScaling, err := rules.ParsePack([]byte(edit(t, goDigitSource(t), icuScalingRule,
		"      - id: no-proportionate-deduction-icu\n"+
			"        clause: IRDAI/HLT/REG/CIR/151/06/2020 para 7\n"+
			"        applies_to: icu\n"+
			"        effect: no-scaling\n")))
	if err != nil {
		t.Fatal(err)
	}

	// A ward-to-ICU stay at ₹1,50,000: the room cap is ₹3,000 a day and the
	// ICU cap ₹7,500, as in golden case 14.
	wardToICU := func(t *testing.T) rules.Facts {
		return facts(rules.AnswerYes,
			[]rules.Segment{seg(t, "2026-01-19", "2026-01-21", rules.SegmentRoom), seg(t, "2026-01-22", "2026-01-24", rules.SegmentICU)},
			ln(t, "L01", rules.CategoryRoomRent, "2026-01-19", 3, 400_000),
			ln(t, "L02", rules.CategoryICUCharges, "2026-01-22", 2, 900_000),
			ln(t, "L03", rules.CategoryProfessionalFees, "2026-01-20", 1, 1_200_000),
			ln(t, "L04", rules.CategoryProfessionalFees, "2026-01-22", 1, 3_000_000),
		)
	}

	tests := []struct {
		name       string
		pack       *rules.Pack
		sumInsured money.Paise
		facts      func(t *testing.T) rules.Facts
		adjs       []wantAdj
		flags      []wantFlag
	}{
		{
			// Nursing on an ICU day at ₹7,000 meets the room cap first
			// (₹5,000), then joins the ICU bucket. The ratio holds it to
			// both caps too: (₹8,000 + ₹10,000) ÷ ₹22,000 = 9/11, not the
			// ICU cap's ₹20,000 ÷ ₹22,000.
			name:       "a mismatched line's own cap lowers the ratio",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-07-01", "2025-07-03", rules.SegmentICU)},
					ln(t, "L01", rules.CategoryICUCharges, "2025-07-01", 2, 400_000),
					ln(t, "L02", rules.CategoryNursing, "2025-07-01", 2, 700_000),
					ln(t, "L03", rules.CategoryProfessionalFees, "2025-07-02", 1, 1_100_000),
				)
			},
			adjs: []wantAdj{
				{"L02", "daily-cap-room", 400_000},
				{"L03", "proportionate-deduction-icu", 200_000},
			},
			flags: []wantFlag{
				{rules.FlagBucketSegmentMismatch, "L02", 0},
				{rules.FlagICURatioScaling, "", 200_000},
			},
		},
		{
			// A stay with one segment has one rate, so an undated room line
			// joins it and nothing is flagged. 1,000,000 × 1,500,000 ÷
			// 1,800,000 = 833,333.33, rounded to 833,333.
			name:       "an undated room line in a one-segment stay joins it",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "", 3, 600_000),
					ln(t, "L02", rules.CategoryProfessionalFees, "2025-06-11", 1, 1_000_000),
				)
			},
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 300_000},
				{"L02", "proportionate-deduction-room", 166_667},
			},
		},
		{
			// Note 3: no scaling. The room is still capped, and the ICU
			// segment's missing charge raises nothing, because no ratio is
			// needed.
			name:       "no differential billing skips scaling and raises no no_room_charge",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerNo,
					[]rules.Segment{seg(t, "2025-06-10", "2025-06-11", rules.SegmentRoom), seg(t, "2025-06-12", "2025-06-13", rules.SegmentICU)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 2, 600_000),
					ln(t, "L02", rules.CategoryProfessionalFees, "2025-06-12", 1, 1_000_000),
				)
			},
			adjs: []wantAdj{{"L01", "daily-cap-room", 200_000}},
		},
		{
			// ADR 0010: a pack can stop scaling by the ICU ratio. Case 14's
			// claim then scales only the ward's line, and raises no
			// icu_ratio_scaling.
			name:       "a no-scaling ICU rule scales nothing in ICU segments",
			pack:       icuNoScaling,
			sumInsured: 15_000_000,
			facts:      wardToICU,
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 300_000},
				{"L02", "daily-cap-icu", 300_000},
				{"L03", "proportionate-deduction-room", 300_000},
			},
		},
		{
			// The ICU at ₹15,000 a day against ₹7,500 has ratio 1/2, below
			// the ward's 3/4, so the undated line takes it, under the ICU
			// rule.
			name:       "an undated line takes the lowest ratio, here the ICU's",
			sumInsured: 15_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes,
					[]rules.Segment{seg(t, "2026-01-19", "2026-01-21", rules.SegmentRoom), seg(t, "2026-01-22", "2026-01-24", rules.SegmentICU)},
					ln(t, "L01", rules.CategoryRoomRent, "2026-01-19", 3, 400_000),
					ln(t, "L02", rules.CategoryICUCharges, "2026-01-22", 2, 1_500_000),
					ln(t, "L03", rules.CategoryProfessionalFees, "", 1, 1_000_000),
				)
			},
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 300_000},
				{"L02", "daily-cap-icu", 1_500_000},
				{"L03", "proportionate-deduction-icu", 500_000},
			},
			flags: []wantFlag{
				{rules.FlagUndatedLine, "L03", 0},
				{rules.FlagICURatioScaling, "", 500_000},
			},
		},
		{
			// Segments with no charge have no ratio: a dated line there
			// isn't scaled, the undated line takes the only ratio there is
			// (2/3), and no_room_charge appears once for the claim.
			name:       "segments with no charge have no ratio and raise no_room_charge once",
			sumInsured: 20_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes,
					[]rules.Segment{
						seg(t, "2025-06-01", "2025-06-02", rules.SegmentRoom),
						seg(t, "2025-06-03", "2025-06-03", rules.SegmentICU),
						seg(t, "2025-06-04", "2025-06-05", rules.SegmentRoom),
					},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-01", 2, 600_000),
					ln(t, "L02", rules.CategoryProfessionalFees, "", 1, 900_000),
					ln(t, "L03", rules.CategoryProfessionalFees, "2025-06-04", 1, 500_000),
				)
			},
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 400_000},
				{"L02", "proportionate-deduction-room", 300_000},
			},
			flags: []wantFlag{
				{rules.FlagUndatedLine, "L02", 0},
				{rules.FlagNoRoomCharge, "", 0},
			},
		},
		{
			// Two room lines that split a 4-day segment cover all 4 days
			// together, so nothing is flagged, the ₹16,000 is under the
			// 4 × ₹5,000 allowance, and nothing is scaled.
			name:       "lines that split a segment count the days they cover together",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 2, 400_000),
					ln(t, "L02", rules.CategoryRoomRent, "2025-06-12", 2, 400_000),
					ln(t, "L03", rules.CategoryProfessionalFees, "2025-06-11", 1, 800_000),
				)
			},
		},
		{
			// Days are judged per category: the room covers 4 of 5 days,
			// which is accepted, but nursing covers only 2, so only the
			// nursing line is flagged. The bucket covers 4 days together.
			name:       "room_days_mismatch is judged per category",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-14", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 4, 300_000),
					ln(t, "L02", rules.CategoryNursing, "2025-06-10", 2, 100_000),
				)
			},
			flags: []wantFlag{{rules.FlagRoomDaysMismatch, "L02", 0}},
		},
		{
			// Session 5: a same-day stay without a room line is day care,
			// and Note 2 applies only after an admission to a room.
			name:       "a same-day stay without a room line raises no no_room_charge",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-08-12", "2025-08-12", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryProfessionalFees, "2025-08-12", 1, 1_000_000),
					ln(t, "L02", rules.CategoryPharmacyConsumables, "2025-08-12", 1, 200_000),
				)
			},
		},
		{
			// The bill has a room line, though a list zeroed it, so the
			// segment has no ratio but isn't missing a room charge.
			name:       "a room line a list zeroed still counts as a room line",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				l := ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 1, 400_000)
				l.ItemList = "list-ii"
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-11", rules.SegmentRoom)},
					l, ln(t, "L02", rules.CategoryProfessionalFees, "2025-06-10", 1, 1_000_000))
			},
			adjs: []wantAdj{{"L01", "annexure-a-list-ii", 400_000}},
		},
		{
			name:       "a segment with no room line and nothing to scale raises nothing",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-11", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryPharmacyConsumables, "2025-06-10", 1, 500_000),
					ln(t, "L02", rules.CategoryDiagnostics, "2025-06-10", 1, 300_000),
				)
			},
		},
		{
			// L01 and L02 both bill the room for 12 June: a day billed
			// twice. The two nursing lines cover the same days, as shifts
			// may, and raise nothing. The bucket is at its allowance, so the
			// cap deducts nothing.
			name:       "two room-rent lines covering the same day are flagged, nursing shifts aren't",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 3, 300_000),
					ln(t, "L02", rules.CategoryRoomRent, "2025-06-12", 1, 300_000),
					ln(t, "L03", rules.CategoryNursing, "2025-06-10", 3, 50_000),
					ln(t, "L04", rules.CategoryNursing, "2025-06-10", 3, 50_000),
				)
			},
			flags: []wantFlag{
				{rules.FlagRoomDaysMismatch, "L01", 0},
				{rules.FlagRoomDaysMismatch, "L02", 0},
			},
		},
		{
			name:       "a same-day stay billed for one day is accepted",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-08-12", "2025-08-12", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-08-12", 1, 300_000),
					ln(t, "L02", rules.CategoryProfessionalFees, "2025-08-12", 1, 1_000_000),
				)
			},
		},
		{
			// The pack lists the package rule first, so it takes the line;
			// the outside-stay rule finds nothing left and raises nothing.
			name:       "a package line outside the stay is set aside once",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				l := ln(t, "L02", rules.CategoryPharmacyConsumables, "2025-06-14", 1, 300_000)
				l.PackageRate = true
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 3, 400_000), l)
			},
			adjs:  []wantAdj{{"L02", "unmodelled-package-rate", 300_000}},
			flags: []wantFlag{{rules.FlagPackageRate, "L02", 0}},
		},
		{
			name:       "a zero-amount package line raises nothing",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				l := ln(t, "L02", rules.CategoryOtherHospitalCharges, "2025-06-11", 1, 0)
				l.PackageRate = true
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 3, 400_000), l)
			},
		},
		{
			name:       "a line dated the day before admission is set aside",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 3, 400_000),
					ln(t, "L02", rules.CategoryDiagnostics, "2025-06-09", 1, 250_000))
			},
			adjs:  []wantAdj{{"L02", "unmodelled-outside-stay", 250_000}},
			flags: []wantFlag{{rules.FlagOutsideStay, "L02", 0}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pack := tt.pack
			if pack == nil {
				pack = goDigit
			}
			f := tt.facts(t)
			res, err := rules.Adjudicate(pack, f, coverOf(t, tt.sumInsured))
			if err != nil {
				t.Fatal(err)
			}
			adjs, flags := early(t, pack, res)
			if !slices.Equal(adjs, tt.adjs) {
				t.Errorf("adjustments\n got %v\nwant %v", adjs, tt.adjs)
			}
			if !slices.Equal(flags, tt.flags) {
				t.Errorf("flags\n got %v\nwant %v", flags, tt.flags)
			}
			checkLines(t, f, res)
		})
	}
}

// TestStepsFourToSix checks the sub-limits, the cover cap, the co-payment,
// the discount, the ledger and proposed_denial on claims the golden cases
// don't cover. It compares every adjustment and flag.
func TestStepsFourToSix(t *testing.T) {
	pack := goDigitPack(t)
	sameDay := func(t *testing.T, discount money.Paise, lines ...rules.Line) rules.Facts {
		// A day-care stay at a hospital without differential billing, so
		// step 3 doesn't run and a bill with no room line raises nothing.
		f := facts(rules.AnswerNo, []rules.Segment{seg(t, "2025-08-12", "2025-08-12", rules.SegmentRoom)}, lines...)
		f.BillDiscount = discount
		return f
	}
	cataract := func(l rules.Line, eye rules.Eye) rules.Line {
		l.Treatment = rules.Treatment{Kind: rules.TreatmentCataract, Eye: eye}
		return l
	}
	tests := []struct {
		name    string
		facts   func(t *testing.T) rules.Facts
		cover   func(c *rules.Cover)
		adjs    []wantAdj
		flags   []wantFlag
		payable money.Paise
		ledger  rules.Ledger
	}{
		{
			// The discount takes the smaller of itself and the payable, so
			// the claim pays nothing, but it isn't a proposed denial: the
			// policy would have paid ₹1,900.
			name: "a discount above the payable takes it to zero, which isn't a proposed denial",
			facts: func(t *testing.T) rules.Facts {
				f := facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-12", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 2, 100_000))
				f.BillDiscount = 500_000
				return f
			},
			adjs:  []wantAdj{{"L01", "co-payment", 10_000}, {"L01", "bill-discount", 190_000}},
			flags: []wantFlag{{rules.FlagBillDiscount, "", 500_000}},
		},
		{
			// With no cover left the claim pays nothing; the co-pay-first
			// figure is also nothing, and the policy pays nothing, so it is
			// a proposed denial.
			name: "no cover left pays nothing and proposes a denial",
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-12", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 2, 100_000))
			},
			cover: func(c *rules.Cover) { c.Tranches[0].SumInsured, c.PaidThisYear = 10_000_000, 10_000_000 },
			adjs:  []wantAdj{{"L01", "cap-cover-left", 200_000}},
			flags: []wantFlag{{rules.FlagCoverExceeded, "", 0}, {rules.FlagProposedDenial, "", 0}},
		},
		{
			// The plan's example: 29 paise of cover against 30 admissible.
			// Cap first: 29 less 5% (1.45, rounded to 1) is 28. Co-pay
			// first: 30 less 5% (1.5, rounded to 2) is 28.
			name: "29 paise of cover against 30 admissible pays 28 either way",
			facts: func(t *testing.T) rules.Facts {
				return sameDay(t, 0, ln(t, "L01", rules.CategoryDiagnostics, "2025-08-12", 1, 30))
			},
			cover:   func(c *rules.Cover) { c.Tranches[0].SumInsured, c.PaidThisYear = 10_000_000, 9_999_971 },
			adjs:    []wantAdj{{"L01", "cap-cover-left", 1}, {"L01", "co-payment", 1}},
			flags:   []wantFlag{{rules.FlagCoverExceeded, "", 28}},
			payable: 28,
			ledger:  rules.Ledger{Paid: 28},
		},
		{
			// ₹12,000 admissible against ₹10,000 of cover, with a ₹1,000
			// discount. Cap first: ₹10,000 − ₹500 − ₹1,000 = ₹8,500. Co-pay
			// first: ₹12,000 − ₹600 = ₹11,400, capped at ₹10,000, less the
			// same ₹1,000 = ₹9,000.
			name: "cover_exceeded's figure takes the same discount",
			facts: func(t *testing.T) rules.Facts {
				return sameDay(t, 100_000, ln(t, "L01", rules.CategoryDiagnostics, "2025-08-12", 1, 1_200_000))
			},
			cover: func(c *rules.Cover) { c.Tranches[0].SumInsured, c.PaidThisYear = 10_000_000, 9_000_000 },
			adjs: []wantAdj{
				{"L01", "cap-cover-left", 200_000}, {"L01", "co-payment", 50_000}, {"L01", "bill-discount", 100_000},
			},
			flags:   []wantFlag{{rules.FlagBillDiscount, "", 100_000}, {rules.FlagCoverExceeded, "", 900_000}},
			payable: 850_000,
			ledger:  rules.Ledger{Paid: 850_000},
		},
		{
			// 25% of the ₹1,00,000 sum insured is ₹25,000; with the
			// ₹50,000 bonus it would be ₹37,500 and cap nothing.
			name: "sub-limit percentages leave out the cumulative bonus",
			facts: func(t *testing.T) rules.Facts {
				return sameDay(t, 0, cataract(ln(t, "L01", rules.CategoryProfessionalFees, "2025-08-12", 1, 3_000_000), rules.EyeRight))
			},
			cover:   func(c *rules.Cover) { c.Tranches[0].SumInsured, c.CumulativeBonus = 10_000_000, 5_000_000 },
			adjs:    []wantAdj{{"L01", "sub-limit-cataract", 500_000}, {"L01", "co-payment", 125_000}},
			payable: 2_375_000,
			ledger:  rules.Ledger{Paid: 2_375_000, CataractUsed: rules.EyeAmounts{Right: 2_500_000}},
		},
		{
			// The left eye used its ₹25,000 earlier in the year, so this
			// claim's left-eye line pays nothing: the policy makes the claim
			// pay nothing, a proposed denial.
			name: "an eye whose limit is used up pays nothing for it",
			facts: func(t *testing.T) rules.Facts {
				return sameDay(t, 0, cataract(ln(t, "L01", rules.CategoryProfessionalFees, "2025-08-12", 1, 1_000_000), rules.EyeLeft))
			},
			cover: func(c *rules.Cover) {
				c.Tranches[0].SumInsured, c.CataractUsed = 10_000_000, rules.EyeAmounts{Left: 2_500_000}
			},
			adjs:  []wantAdj{{"L01", "sub-limit-cataract", 1_000_000}},
			flags: []wantFlag{{rules.FlagProposedDenial, "", 0}},
		},
		{
			// The cover cap binds after the sub-limit: the eye's use is what
			// its line was admitted at before the co-payment, ₹20,000.
			// Co-pay first: ₹25,000 − ₹1,250 = ₹23,750, capped at ₹20,000.
			name: "cataract use is counted after the cover cap and before the co-payment",
			facts: func(t *testing.T) rules.Facts {
				return sameDay(t, 0, cataract(ln(t, "L01", rules.CategoryProfessionalFees, "2025-08-12", 1, 2_500_000), rules.EyeRight))
			},
			cover:   func(c *rules.Cover) { c.Tranches[0].SumInsured, c.PaidThisYear = 10_000_000, 8_000_000 },
			adjs:    []wantAdj{{"L01", "cap-cover-left", 500_000}, {"L01", "co-payment", 100_000}},
			flags:   []wantFlag{{rules.FlagCoverExceeded, "", 2_000_000}},
			payable: 1_900_000,
			ledger:  rules.Ledger{Paid: 1_900_000, CataractUsed: rules.EyeAmounts{Right: 2_000_000}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.facts(t)
			c := coverOf(t, 50_000_000)
			if tt.cover != nil {
				tt.cover(&c)
			}
			res, err := rules.Adjudicate(pack, f, c)
			if err != nil {
				t.Fatal(err)
			}
			var adjs []wantAdj
			for _, a := range res.Adjustments {
				adjs = append(adjs, wantAdj{a.LineID, a.RuleID, a.Amount})
			}
			var flags []wantFlag
			for _, fl := range res.Flags {
				flags = append(flags, wantFlag{fl.Code, fl.LineID, fl.Figure})
			}
			if !slices.Equal(adjs, tt.adjs) {
				t.Errorf("adjustments\n got %v\nwant %v", adjs, tt.adjs)
			}
			if !slices.Equal(flags, tt.flags) {
				t.Errorf("flags\n got %v\nwant %v", flags, tt.flags)
			}
			if res.Payable != tt.payable || res.Ledger != tt.ledger {
				t.Errorf("payable %d, ledger %+v; want %d and %+v", res.Payable, res.Ledger, tt.payable, tt.ledger)
			}
			checkLines(t, f, res)
		})
	}
}

// TestEligibility checks the waiting periods on claims the golden cases
// don't cover. Each claim is a 2-day stay: a ₹4,000 room for 1 day and a
// ₹30,000 fee. Eligible on ₹3,00,000 it pays ₹32,300.
func TestEligibility(t *testing.T) {
	pack := goDigitPack(t)
	stay := func(t *testing.T, admission string, cond rules.Condition) rules.Facts {
		d := date(t, admission)
		next, err := d.AddDays(1)
		if err != nil {
			t.Fatal(err)
		}
		f := facts(rules.AnswerYes, []rules.Segment{{From: d, To: next, Kind: rules.SegmentRoom}},
			ln(t, "L01", rules.CategoryRoomRent, admission, 1, 400_000),
			ln(t, "L02", rules.CategoryProfessionalFees, admission, 1, 3_000_000))
		f.Condition = cond
		return f
	}
	tranche := func(t *testing.T, sumInsured money.Paise, since string) rules.Tranche {
		return rules.Tranche{SumInsured: sumInsured, ContinuousSince: date(t, since)}
	}
	eligible := []wantAdj{{"L01", "co-payment", 20_000}, {"L02", "co-payment", 150_000}}
	excluded := func(rule string) []wantAdj { return []wantAdj{{"L01", rule, 400_000}, {"L02", rule, 3_000_000}} }

	tests := []struct {
		name     string
		facts    func(t *testing.T) rules.Facts
		tranches func(t *testing.T) []rules.Tranche
		adjs     []wantAdj
		flags    []wantFlag
		payable  money.Paise
	}{
		{
			// A hernia inside its 24-month wait, but from an accident:
			// Excl02(a) doesn't apply.
			name: "an accident lifts a listed condition's wait",
			facts: func(t *testing.T) rules.Facts {
				return stay(t, "2025-08-04", rules.Condition{ListedGroup: "listed-24-months", Accident: true})
			},
			tranches: func(t *testing.T) []rules.Tranche { return []rules.Tranche{tranche(t, 30_000_000, "2025-01-01")} },
			adjs:     eligible,
			payable:  3_230_000,
		},
		{
			name: "an accident doesn't lift the pre-existing disease wait",
			facts: func(t *testing.T) rules.Facts {
				return stay(t, "2025-08-04", rules.Condition{PreExisting: true, Accident: true})
			},
			tranches: func(t *testing.T) []rules.Tranche { return []rules.Tranche{tranche(t, 30_000_000, "2024-01-01")} },
			adjs:     excluded("waiting-period-pre-existing"),
			flags:    []wantFlag{{rules.FlagTrancheExcluded, "", 30_000_000}, {rules.FlagProposedDenial, "", 0}},
		},
		{
			// Excl03(iii): the 30 days apply afresh to an increase, here on
			// its day 16. Only the ₹1,00,000 base counts, so the room cap is
			// 2% of it, ₹2,000: the room is capped at ₹2,000 and the fee
			// scaled by ₹2,000 ÷ ₹4,000.
			name:  "the 30-day wait applies afresh to a sum-insured increase",
			facts: func(t *testing.T) rules.Facts { return stay(t, "2025-08-04", rules.Condition{}) },
			tranches: func(t *testing.T) []rules.Tranche {
				return []rules.Tranche{tranche(t, 10_000_000, "2023-04-01"), tranche(t, 20_000_000, "2025-07-20")}
			},
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 200_000}, {"L02", "proportionate-deduction-room", 1_500_000},
				{"L01", "co-payment", 10_000}, {"L02", "co-payment", 75_000},
			},
			flags:   []wantFlag{{rules.FlagTrancheExcluded, "", 20_000_000}},
			payable: 1_615_000,
		},
		{
			// When the onset is known it decides the 30-day wait, and an
			// onset before cover began isn't inside it; a pre-existing
			// disease is for Excl01, which the facts don't claim here.
			name: "an onset before cover began isn't inside the 30-day wait",
			facts: func(t *testing.T) rules.Facts {
				return stay(t, "2025-05-10", rules.Condition{Onset: date(t, "2025-04-20")})
			},
			tranches: func(t *testing.T) []rules.Tranche { return []rules.Tranche{tranche(t, 30_000_000, "2025-05-01")} },
			adjs:     eligible,
			payable:  3_230_000,
		},
		{
			// Excl03(ii) and (iii): the increase's onset is on its day 20,
			// and the admission is on the last day of its first 12 months,
			// so its 30-day exclusion stands. Only the base counts, as in
			// the test above.
			name: "an increase's onset in its 30 days excludes it through its 12th month",
			facts: func(t *testing.T) rules.Facts {
				return stay(t, "2026-04-30", rules.Condition{Onset: date(t, "2025-05-20")})
			},
			tranches: func(t *testing.T) []rules.Tranche {
				return []rules.Tranche{tranche(t, 10_000_000, "2023-04-01"), tranche(t, 20_000_000, "2025-05-01")}
			},
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 200_000}, {"L02", "proportionate-deduction-room", 1_500_000},
				{"L01", "co-payment", 10_000}, {"L02", "co-payment", 75_000},
			},
			flags:   []wantFlag{{rules.FlagTrancheExcluded, "", 20_000_000}},
			payable: 1_615_000,
		},
		{
			// The next day the increase has run for more than 12 months, so
			// Excl03(ii) lifts its 30 days, though the onset is inside them.
			name: "an increase's onset in its 30 days stops excluding it after 12 months",
			facts: func(t *testing.T) rules.Facts {
				return stay(t, "2026-05-01", rules.Condition{Onset: date(t, "2025-05-20")})
			},
			tranches: func(t *testing.T) []rules.Tranche {
				return []rules.Tranche{tranche(t, 10_000_000, "2023-04-01"), tranche(t, 20_000_000, "2025-05-01")}
			},
			adjs:    eligible,
			payable: 3_230_000,
		},
		{
			// Both tranches are inside a hernia's 24-month wait: the claim is
			// a proposed denial, every line cites the oldest tranche's wait,
			// and tranche_excluded carries the whole ₹3,00,000.
			name: "with no tranche eligible, the whole sum insured is excluded",
			facts: func(t *testing.T) rules.Facts {
				return stay(t, "2025-08-04", rules.Condition{ListedGroup: "listed-24-months"})
			},
			tranches: func(t *testing.T) []rules.Tranche {
				return []rules.Tranche{tranche(t, 10_000_000, "2024-01-01"), tranche(t, 20_000_000, "2025-04-01")}
			},
			adjs:  excluded("waiting-period-listed-24-months"),
			flags: []wantFlag{{rules.FlagTrancheExcluded, "", 30_000_000}, {rules.FlagProposedDenial, "", 0}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.facts(t)
			res, err := rules.Adjudicate(pack, f, rules.Cover{Tranches: tt.tranches(t)})
			if err != nil {
				t.Fatal(err)
			}
			var adjs []wantAdj
			for _, a := range res.Adjustments {
				adjs = append(adjs, wantAdj{a.LineID, a.RuleID, a.Amount})
				if r, _ := pack.Rule(a.RuleID); a.Clause != r.Clause {
					t.Errorf("%s cites %q, want %q", a.RuleID, a.Clause, r.Clause)
				}
			}
			var flags []wantFlag
			for _, fl := range res.Flags {
				flags = append(flags, wantFlag{fl.Code, fl.LineID, fl.Figure})
			}
			if !slices.Equal(adjs, tt.adjs) {
				t.Errorf("adjustments\n got %v\nwant %v", adjs, tt.adjs)
			}
			if !slices.Equal(flags, tt.flags) {
				t.Errorf("flags\n got %v\nwant %v", flags, tt.flags)
			}
			if res.Payable != tt.payable {
				t.Errorf("payable %d, want %d", res.Payable, tt.payable)
			}
			checkLines(t, f, res)
		})
	}
}
