package rulepacks_test

import (
	"crypto/sha256"
	"encoding/hex"
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

// published pins the SHA-256 of every published pack version. A published
// pack never changes: a change to one, even to a comment, means a new
// version in a new file, pinned here when it is published. Never edit a pin
// to make this test pass. .gitattributes keeps the packs LF on every
// platform, so a hash is the same on every machine and CI job.
var published = []struct {
	path    string
	id      string
	version int64
	sha256  string
}{
	// Published at the M1 gate, week 2 session 7, 24 November 2026, with
	// ADR 0012's sub-limit order.
	{"arogya-sanjeevani/godigit-2024.yaml", "arogya-sanjeevani-godigit-2024", 1,
		"731dbeb1ea9e3c14cb79b5707d8dfb09fc9f1ca02eee051008f98034c39295eb"},
}

func TestPublishedPacks(t *testing.T) {
	pinned := map[string]bool{}
	for _, pub := range published {
		pinned[pub.path] = true
		src, err := rulepacks.Source(pub.path)
		if err != nil {
			t.Errorf("published pack %s: %v", pub.path, err)
			continue
		}
		sum := sha256.Sum256(src)
		if got := hex.EncodeToString(sum[:]); got != pub.sha256 {
			t.Errorf("%s has changed since %s version %d was published: its SHA-256 is %s, pinned %s. "+
				"A published pack never changes; put the change in a new file as a new version.",
				pub.path, pub.id, pub.version, got, pub.sha256)
		}
		p, err := rules.ParsePack(src)
		if err != nil {
			t.Errorf("published pack %s: %v", pub.path, err)
			continue
		}
		if p.ID != pub.id || p.Version != pub.version {
			t.Errorf("%s is %s, want %s version %d", pub.path, p.Identity(), pub.id, pub.version)
		}
	}
	// Every pack the registry offers is a published one.
	paths, err := rulepacks.Paths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if !pinned[path] {
			t.Errorf("%s isn't published: pin its SHA-256 in published when its gate publishes it", path)
		}
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

	// Excl03(i) and Excl02(a) don't apply to accidents; Excl01 does.
	waits := []struct {
		rule           string
		clause         string
		days, months   int64
		waivedAfterMos int64
		accident       bool
	}{
		{rule: "waiting-period-first-30-days", clause: "Excl03", days: 30, waivedAfterMos: 12, accident: true},
		{rule: "waiting-period-listed-24-months", clause: "Excl02", months: 24, accident: true},
		{rule: "waiting-period-listed-36-months", clause: "Excl02", months: 36, accident: true},
		{rule: "waiting-period-pre-existing", clause: "Excl01", months: 36},
	}
	for _, w := range waits {
		r, _ := p.Rule(w.rule)
		if r.Clause != w.clause || r.Days != w.days || r.Months != w.months || r.WaivedAfterMonths != w.waivedAfterMos ||
			r.WaivedForAccident != w.accident {
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
