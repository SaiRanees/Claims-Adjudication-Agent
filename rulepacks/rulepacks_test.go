package rulepacks_test

import (
	"slices"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

func TestRegistry(t *testing.T) {
	paths, err := rulepacks.Paths()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, "arogya-sanjeevani/godigit-2024.yaml") {
		t.Errorf("Paths() = %v, want the Go Digit pack", paths)
	}
	r, err := rulepacks.Registry()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(r.Packs()); got != len(paths) {
		t.Errorf("registry has %d packs, want %d", got, len(paths))
	}
}

func goDigit(t *testing.T) *rules.Pack {
	t.Helper()
	r, err := rulepacks.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range r.Packs() {
		if p.ID == "arogya-sanjeevani-godigit-2024" {
			return p
		}
	}
	t.Fatal("no Go Digit pack")
	return nil
}

// TestGoDigitLimits restates the wording's limits, read from the PDF by hand,
// so an edit to the pack that changes one fails here as well.
func TestGoDigitLimits(t *testing.T) {
	p := goDigit(t)
	if p.Version != 1 || p.Wording.UIN != "GODHLIP24179V022324" {
		t.Errorf("identity = %s, UIN %s", p.Identity(), p.Wording.UIN)
	}
	if got := p.Wording.ValidFrom.String(); got != "2023-04-01" {
		t.Errorf("valid_from = %s", got)
	}
	if !p.Wording.ValidUntil.IsZero() {
		t.Errorf("valid_until = %s, want none", p.Wording.ValidUntil)
	}

	limit := func(share money.BasisPoints, ceiling money.Paise) rules.Limit {
		return rules.Limit{Share: share, HasShare: share != 0, Ceiling: ceiling, HasCeiling: ceiling != 0}
	}
	tests := []struct {
		rule   string
		clause string
		limit  rules.Limit
		per    rules.Period
	}{
		{rule: "daily-cap-room", clause: "C.I.1(i)", limit: limit(200, 5_000_00)},
		{rule: "daily-cap-icu", clause: "C.I.1(ii)", limit: limit(500, 10_000_00)},
		{rule: "sub-limit-cataract", clause: "C.I.3", limit: limit(2_500, 40_000_00), per: rules.PerEyePerPolicyYear},
		{rule: "sub-limit-modern-treatment", clause: "C.I.6", limit: limit(5_000, 0), per: rules.PerPolicyYear},
		{rule: "sub-limit-road-ambulance", clause: "C.I.1.1(v)", limit: limit(0, 2_000_00), per: rules.PerHospitalisation},
	}
	for _, tt := range tests {
		r, ok := p.Rule(tt.rule)
		if !ok {
			t.Errorf("no rule %s", tt.rule)
			continue
		}
		if r.Clause != tt.clause || r.Limit != tt.limit || r.Per != tt.per {
			t.Errorf("%s = clause %s, limit %+v, per %q; want %s, %+v, %q",
				tt.rule, r.Clause, r.Limit, r.Per, tt.clause, tt.limit, tt.per)
		}
	}

	if r, _ := p.Rule("co-payment"); r.Rate != 500 || r.Clause != "E.II.28(c)" {
		t.Errorf("co-payment = %d bp citing %s, want 500 citing E.II.28(c)", r.Rate, r.Clause)
	}

	waits := []struct {
		rule           string
		clause         string
		days, months   int64
		waivedAfterMos int64
	}{
		{rule: "waiting-period-first-30-days", clause: "Excl03", days: 30, waivedAfterMos: 12},
		{rule: "waiting-period-listed-24-months", clause: "Excl02", months: 24},
		{rule: "waiting-period-listed-36-months", clause: "Excl02", months: 36},
		{rule: "waiting-period-pre-existing", clause: "Excl01", months: 36},
	}
	for _, w := range waits {
		r, _ := p.Rule(w.rule)
		if r.Clause != w.clause || r.Days != w.days || r.Months != w.months || r.WaivedAfterMonths != w.waivedAfterMos {
			t.Errorf("%s = %+v", w.rule, r)
		}
	}
}

// TestGoDigitPages pins the pages of the passages the plan and ADRs 0009 and
// 0010 quote, as confirmed against the 25-page wording and the 2020 circular.
func TestGoDigitPages(t *testing.T) {
	p := goDigit(t)
	tests := []struct {
		clause   string
		document string
		page     int64
		lastPage int64
	}{
		{clause: "E.II.28(c)", document: "wording", page: 18},
		{clause: "Definition 10", document: "wording", page: 2, lastPage: 3},
		{clause: "Definition 26", document: "wording", page: 4},
		{clause: "IRDAI/HLT/REG/CIR/151/06/2020 para 7", document: "irdai-circular-151-2020", page: 1},
		{clause: "C.I.1 Note 2", document: "wording", page: 6},
		{clause: "Operative clause", document: "wording", page: 1},
	}
	for _, tt := range tests {
		c, ok := p.Clause(tt.clause)
		if !ok {
			t.Errorf("no clause %s", tt.clause)
			continue
		}
		if c.Document != tt.document || c.Page != tt.page || c.LastPage != tt.lastPage {
			t.Errorf("%s = %s pages %d to %d, want %s pages %d to %d",
				tt.clause, c.Document, c.Page, c.LastPage, tt.document, tt.page, tt.lastPage)
		}
	}
}

// TestGoDigitTreatments checks where each category sits, and the choices the
// plan made about proportionate deduction.
func TestGoDigitTreatments(t *testing.T) {
	p := goDigit(t)
	room, _ := p.Rule("daily-cap-room")
	icu, _ := p.Rule("daily-cap-icu")
	if !slices.Equal(room.Categories, []rules.Category{rules.CategoryRoomRent, rules.CategoryNursing, rules.CategoryPatientDiet}) {
		t.Errorf("room bucket = %v", room.Categories)
	}
	if !slices.Equal(icu.Categories, []rules.Category{rules.CategoryICUCharges}) {
		t.Errorf("ICU bucket = %v", icu.Categories)
	}
	var pd rules.Step
	for _, s := range p.Steps {
		if s.Name == rules.StepApplyProportionateDeduction {
			pd = s
		}
	}
	if pd.RatioBasis != rules.RatioBucketDailyTotal {
		t.Errorf("ratio basis = %q", pd.RatioBasis)
	}
	wantExempt := []rules.Category{rules.CategoryPharmacyConsumables, rules.CategoryImplantsDevices, rules.CategoryDiagnostics, rules.CategoryRoadAmbulance}
	if !slices.Equal(pd.ExemptCategories, wantExempt) {
		t.Errorf("exempt = %v, want %v", pd.ExemptCategories, wantExempt)
	}
	roomScaling, _ := p.Rule("proportionate-deduction-room")
	if roomScaling.Effect != rules.EffectScale {
		t.Errorf("proportionate-deduction-room effect = %q, want scale", roomScaling.Effect)
	}
	icuScaling, _ := p.Rule("proportionate-deduction-icu")
	if icuScaling.Clause != "C.I.1 Note 2" || icuScaling.Effect != rules.EffectScale ||
		!slices.Contains(icuScaling.SeeAlso, "IRDAI/HLT/REG/CIR/151/06/2020 para 7") {
		t.Errorf("proportionate-deduction-icu = %+v", icuScaling)
	}
	for _, id := range []string{"unmodelled-package-rate", "unmodelled-outside-stay"} {
		if r, _ := p.Rule(id); !r.Unmodelled || r.Clause != "" {
			t.Errorf("%s = %+v, want unmodelled with no clause", id, r)
		}
	}
}
