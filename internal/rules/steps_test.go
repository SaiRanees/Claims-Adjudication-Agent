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
			// Two room lines that split a 4-day segment each bill 2 days, so
			// both are flagged, and the bucket counts the most days any line
			// bills: 2, at or below the 4 a reviewer may find. The ratio is
			// ₹10,000 ÷ ₹16,000.
			name:       "lines that split a segment count the most days any line bills",
			sumInsured: 50_000_000,
			facts: func(t *testing.T) rules.Facts {
				return facts(rules.AnswerYes, []rules.Segment{seg(t, "2025-06-10", "2025-06-13", rules.SegmentRoom)},
					ln(t, "L01", rules.CategoryRoomRent, "2025-06-10", 2, 400_000),
					ln(t, "L02", rules.CategoryRoomRent, "2025-06-12", 2, 400_000),
					ln(t, "L03", rules.CategoryProfessionalFees, "2025-06-11", 1, 800_000),
				)
			},
			adjs: []wantAdj{
				{"L01", "daily-cap-room", 300_000},
				{"L02", "daily-cap-room", 300_000},
				{"L03", "proportionate-deduction-room", 300_000},
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
