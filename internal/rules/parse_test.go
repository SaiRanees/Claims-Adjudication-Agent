package rules_test

import (
	"crypto/sha256"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

const goDigitPath = "arogya-sanjeevani/godigit-2024.yaml"

func goDigitSource(t testing.TB) string {
	t.Helper()
	src, err := rulepacks.Source(goDigitPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// edit replaces the one occurrence of old in src. It fails the test if old
// doesn't occur exactly once, so a variant can't silently stop testing
// anything when the pack changes.
func edit(t testing.TB, src, old, replacement string) string {
	t.Helper()
	if n := strings.Count(src, old); n != 1 {
		t.Fatalf("%q occurs %d times in the pack, want 1", old, n)
	}
	return strings.Replace(src, old, replacement, 1)
}

func TestParsePackGoDigit(t *testing.T) {
	src := goDigitSource(t)
	p, err := rules.ParsePack([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	id := p.Identity()
	if id.ID != "arogya-sanjeevani-godigit-2024" || id.Version != 1 {
		t.Errorf("identity = %s", id)
	}
	if id.SHA256 != sha256.Sum256([]byte(src)) {
		t.Errorf("identity SHA-256 = %x, want the SHA-256 of the source", id.SHA256)
	}
	var names []rules.StepName
	for _, s := range p.Steps {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, rules.StepNames()) {
		t.Errorf("steps = %v, want %v", names, rules.StepNames())
	}
}

// malformed is one edit to the real pack that ParsePack must reject, with the
// error and the field it must name.
type malformed struct {
	name        string
	old, new    string
	wantErr     error
	wantPath    string
	appendToEnd bool // add new to the end of the pack instead of editing
}

var malformedCases = []malformed{
	// Unknown and misplaced fields.
	{name: "unknown top-level field", old: "version: 1\n", new: "version: 1\nrevision: 2\n",
		wantErr: rules.ErrUnknownField, wantPath: "revision"},
	{name: "misspelt rule field", old: "        ceiling_paise: 500000\n", new: "        ceiling_paise: 500000\n        ceilng_paise: 1\n",
		wantErr: rules.ErrUnknownField, wantPath: "steps[3].rules[0].ceilng_paise"},
	{name: "another step's rule field", old: "        rate_bp: 500\n", new: "        rate_bp: 500\n        per: policy-year\n",
		wantErr: rules.ErrUnknownField, wantPath: "steps[7].rules[0].per"},
	{name: "proportionate-deduction field on another step", old: "  - step: apply-sub-limits\n", new: "  - step: apply-sub-limits\n    ratio_basis: bucket-daily-total\n",
		wantErr: rules.ErrUnknownField, wantPath: "steps[5].ratio_basis"},
	{name: "duplicate key", old: "version: 1\n", new: "version: 1\nversion: 2\n",
		wantErr: rules.ErrDuplicateKey, wantPath: "version"},
	{name: "missing field", old: "  valid_from: 2023-04-01\n", new: "",
		wantErr: rules.ErrMissingField, wantPath: "wording.valid_from"},

	// Floats.
	{name: "float rate", old: "rate_bp: 500\n", new: "rate_bp: 5.0\n",
		wantErr: rules.ErrFloat, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "float in exponent form", old: "share_of_sum_insured_bp: 200\n", new: "share_of_sum_insured_bp: 2e2\n",
		wantErr: rules.ErrFloat, wantPath: "steps[3].rules[0].share_of_sum_insured_bp"},
	{name: "float infinity", old: "ceiling_paise: 200000\n", new: "ceiling_paise: .inf\n",
		wantErr: rules.ErrFloat, wantPath: "steps[5].rules[2].ceiling_paise"},
	{name: "float in a string field", old: "    title: Co-payment\n", new: "    title: 2.5\n",
		wantErr: rules.ErrFloat, wantPath: "clauses[1].title"},

	// Integers not of the form ^(0|[1-9][0-9]*)$.
	{name: "plus sign", old: "rate_bp: 500\n", new: "rate_bp: +500\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "minus sign", old: "rate_bp: 500\n", new: "rate_bp: -500\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "leading zero", old: "rate_bp: 500\n", new: "rate_bp: 0500\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "hexadecimal", old: "rate_bp: 500\n", new: "rate_bp: 0x1F4\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "octal", old: "rate_bp: 500\n", new: "rate_bp: 0o764\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "underscore", old: "rate_bp: 500\n", new: "rate_bp: 5_00\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "quoted integer", old: "rate_bp: 500\n", new: "rate_bp: \"500\"\n",
		wantErr: rules.ErrInteger, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "integer over int64", old: "ceiling_paise: 200000\n", new: "ceiling_paise: 9223372036854775808\n",
		wantErr: rules.ErrInteger, wantPath: "steps[5].rules[2].ceiling_paise"},
	{name: "leading zero version", old: "version: 1\n", new: "version: 01\n",
		wantErr: rules.ErrInteger, wantPath: "version"},

	// Documents, anchors, aliases, merge keys and tags.
	{name: "second document", new: "---\nid: another\n", appendToEnd: true,
		wantErr: rules.ErrMultipleDocuments, wantPath: ""},
	{name: "anchor", old: "  uin: GODHLIP24179V022324\n", new: "  uin: &uin GODHLIP24179V022324\n",
		wantErr: rules.ErrAnchor, wantPath: "wording.uin"},
	// The anchor comes first, but the alias is what's reported.
	{name: "alias", old: "        rate_bp: 500\n", new: "        rate_bp: &rate 500\n        per: *rate\n",
		wantErr: rules.ErrAlias, wantPath: "steps[7].rules[0].per"},
	{name: "merge key", old: "      - id: daily-cap-icu\n", new: "      - id: daily-cap-icu\n        <<: {per: policy-year}\n",
		wantErr: rules.ErrMergeKey, wantPath: "steps[3].rules[1].<<"},
	{name: "explicit tag", old: "rate_bp: 500\n", new: "rate_bp: !!int 500\n",
		wantErr: rules.ErrTag, wantPath: "steps[7].rules[0].rate_bp"},
	{name: "not YAML", new: "\n  : : [\n", appendToEnd: true,
		wantErr: rules.ErrSyntax, wantPath: ""},

	// Clauses.
	{name: "undefined clause", old: "        clause: C.I.3\n", new: "        clause: C.I.4\n",
		wantErr: rules.ErrUndefinedClause, wantPath: "steps[5].rules[0].clause"},
	{name: "undefined see_also clause", old: "see_also: [Definition 40]", new: "see_also: [Definition 41]",
		wantErr: rules.ErrUndefinedClause, wantPath: "steps[3].rules[0].see_also[0]"},
	{name: "no clause and not unmodelled", old: "        clause: Definition 26\n", new: "",
		wantErr: rules.ErrNoClause, wantPath: "steps[8].rules[0].clause"},
	{name: "unmodelled false and no clause", old: "        unmodelled: true\n        applies_to: outside-stay\n", new: "        unmodelled: false\n        applies_to: outside-stay\n",
		wantErr: rules.ErrNoClause, wantPath: "steps[2].rules[1].clause"},
	{name: "unmodelled with a clause", old: "        unmodelled: true\n        applies_to: package-rate\n", new: "        unmodelled: true\n        clause: C.I.7\n        applies_to: package-rate\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[2].rules[0].clause"},
	{name: "duplicate clause ID", old: "  - id: Definition 24\n", new: "  - id: Definition 10\n",
		wantErr: rules.ErrDuplicateID, wantPath: "clauses[2].id"},
	{name: "page past the document", old: "    page: 18\n", new: "    page: 26\n",
		wantErr: rules.ErrInvalidValue, wantPath: "clauses[16].page"},
	{name: "undefined document", old: "    document: irdai-circular-151-2020\n", new: "    document: irdai-circular\n",
		wantErr: rules.ErrInvalidValue, wantPath: "clauses[22].document"},

	// Categories.
	{name: "category listed twice", old: "diagnostics, road-ambulance]", new: "diagnostics, road-ambulance, nursing]",
		wantErr: rules.ErrCategory, wantPath: "steps[4].exempt_categories[4]"},
	{name: "category not listed", old: "diagnostics, road-ambulance]", new: "diagnostics]",
		wantErr: rules.ErrCategory, wantPath: "steps"},
	{name: "unknown category", old: "diagnostics, road-ambulance]", new: "diagnostic, road-ambulance]",
		wantErr: rules.ErrCategory, wantPath: "steps[4].exempt_categories[2]"},

	// Steps and targets.
	{name: "step missing", old: "  - step: take-bill-discount\n    rules:\n      - id: bill-discount\n        clause: Definition 26\n        applies_to: claim\n", new: "",
		wantErr: rules.ErrStep, wantPath: "steps"},
	{name: "step repeated", old: "  - step: take-bill-discount\n", new: "  - step: take-co-payment\n",
		wantErr: rules.ErrStep, wantPath: "steps[8].step"},
	{name: "unknown step", old: "  - step: take-bill-discount\n", new: "  - step: take-discount\n",
		wantErr: rules.ErrStep, wantPath: "steps[8].step"},
	{name: "target missing", old: "      - id: annexure-a-list-v\n        clause: Annexure A List V\n        applies_to: list-v\n        effect: not-payable\n", new: "",
		wantErr: rules.ErrTarget, wantPath: "steps[1].rules"},
	{name: "target repeated", old: "applies_to: list-v\n", new: "applies_to: list-iv\n",
		wantErr: rules.ErrTarget, wantPath: "steps[1].rules[4].applies_to"},
	{name: "unknown target", old: "applies_to: road-ambulance\n", new: "applies_to: air-ambulance\n",
		wantErr: rules.ErrTarget, wantPath: "steps[5].rules[2].applies_to"},
	{name: "duplicate rule ID", old: "      - id: annexure-a-list-v\n", new: "      - id: annexure-a-list-iv\n",
		wantErr: rules.ErrDuplicateID, wantPath: "steps[1].rules[4].id"},

	// Values.
	{name: "invalid date", old: "valid_from: 2023-04-01\n", new: "valid_from: 2021-02-29\n",
		wantErr: rules.ErrInvalidValue, wantPath: "wording.valid_from"},
	{name: "valid_until before valid_from", old: "  valid_from: 2023-04-01\n", new: "  valid_from: 2023-04-01\n  valid_until: 2020-04-15\n",
		wantErr: rules.ErrInvalidValue, wantPath: "wording.valid_until"},
	{name: "list where a value goes", old: "    pages: 25\n", new: "    pages: [25]\n",
		wantErr: rules.ErrType, wantPath: "documents[0].pages"},
	{name: "share over 100%", old: "share_of_sum_insured_bp: 5000\n", new: "share_of_sum_insured_bp: 10001\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[5].rules[1].share_of_sum_insured_bp"},
	{name: "days and months", old: "        days: 30\n", new: "        days: 30\n        months: 1\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[0].rules[0].months"},
	{name: "sub-limit with no limit", old: "        ceiling_paise: 200000\n", new: "",
		wantErr: rules.ErrMissingField, wantPath: "steps[5].rules[2].ceiling_paise"},
	{name: "unknown list effect", old: "effect: treatment-costs\n", new: "effect: treatment-cost\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[1].rules[3].effect"},
	{name: "unknown ratio basis", old: "ratio_basis: bucket-daily-total\n", new: "ratio_basis: room-rent-only\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[4].ratio_basis"},
	{name: "lower-case UIN", old: "  uin: GODHLIP24179V022324\n", new: "  uin: godhlip24179v022324\n",
		wantErr: rules.ErrInvalidValue, wantPath: "wording.uin"},

	// Proportionate-deduction effects.
	{name: "proportionate-deduction rule with no effect", old: "        applies_to: icu\n        effect: scale\n", new: "        applies_to: icu\n",
		wantErr: rules.ErrMissingField, wantPath: "steps[4].rules[1].effect"},
	{name: "unknown scaling effect", old: "        applies_to: room\n        effect: scale\n", new: "        applies_to: room\n        effect: scaled\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[4].rules[0].effect"},
	{name: "list effect on a proportionate-deduction rule", old: "        applies_to: room\n        effect: scale\n", new: "        applies_to: room\n        effect: not-payable\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[4].rules[0].effect"},
	{name: "unmodelled no-scaling rule", old: icuScalingRule, new: "      - id: no-proportionate-deduction-icu\n        unmodelled: true\n        applies_to: icu\n        effect: no-scaling\n",
		wantErr: rules.ErrInvalidValue, wantPath: "steps[4].rules[1].unmodelled"},
}

// icuScalingRule is the pack's rule for scaling by an ICU ratio.
const icuScalingRule = "      - id: proportionate-deduction-icu\n" +
	"        clause: C.I.1 Note 2\n" +
	"        see_also: [C.I.1 Note 3, IRDAI/HLT/REG/CIR/151/06/2020 para 7]\n" +
	"        applies_to: icu\n" +
	"        effect: scale\n"

func (m malformed) apply(t testing.TB, src string) string {
	t.Helper()
	if m.appendToEnd {
		return src + m.new
	}
	return edit(t, src, m.old, m.new)
}

func TestParsePackMalformed(t *testing.T) {
	src := goDigitSource(t)
	for _, tt := range malformedCases {
		t.Run(tt.name, func(t *testing.T) {
			p, err := rules.ParsePack([]byte(tt.apply(t, src)))
			if err == nil {
				t.Fatalf("ParsePack accepted the pack (%s)", p.Identity())
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			var fe *rules.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("error %v isn't a *FieldError", err)
			}
			if fe.Path != tt.wantPath {
				t.Errorf("error names %q, want %q (%v)", fe.Path, tt.wantPath, err)
			}
		})
	}
}

// The parser is strict about form, not fussy about style: these edits keep
// the pack valid.
func TestParsePackValidVariants(t *testing.T) {
	src := goDigitSource(t)
	tests := []struct {
		name     string
		old, new string
		check    func(t *testing.T, p *rules.Pack)
	}{
		{name: "lone zero", old: "rate_bp: 500\n", new: "rate_bp: 0\n", check: func(t *testing.T, p *rules.Pack) {
			if r, _ := p.Rule("co-payment"); r.Rate != 0 {
				t.Errorf("rate = %d, want 0", r.Rate)
			}
		}},
		{name: "quoted string", old: "  uin: GODHLIP24179V022324\n", new: "  uin: \"GODHLIP24179V022324\"\n", check: func(t *testing.T, p *rules.Pack) {
			if p.Wording.UIN != "GODHLIP24179V022324" {
				t.Errorf("UIN = %q", p.Wording.UIN)
			}
		}},
		{name: "quoted date", old: "valid_from: 2023-04-01\n", new: "valid_from: \"2023-04-01\"\n", check: func(t *testing.T, p *rules.Pack) {
			if got := p.Wording.ValidFrom.String(); got != "2023-04-01" {
				t.Errorf("valid_from = %s", got)
			}
		}},
		{name: "sections in another order", old: "id: arogya-sanjeevani-godigit-2024\nversion: 1\n", new: "version: 1\nid: arogya-sanjeevani-godigit-2024\n", check: func(t *testing.T, p *rules.Pack) {
			if p.ID != "arogya-sanjeevani-godigit-2024" || p.Version != 1 {
				t.Errorf("identity = %s", p.Identity())
			}
		}},
		{name: "valid_until", old: "  valid_from: 2023-04-01\n", new: "  valid_from: 2023-04-01\n  valid_until: 2030-03-31\n", check: func(t *testing.T, p *rules.Pack) {
			if got := p.Wording.ValidUntil.String(); got != "2030-03-31" {
				t.Errorf("valid_until = %s", got)
			}
		}},
		// ADR 0010: a later version can stop scaling by an ICU ratio by
		// changing only the pack. The no-scaling rule still covers the ICU
		// target, and cites its reason.
		{
			name: "ICU scaling dropped",
			old:  icuScalingRule,
			new: "      - id: no-proportionate-deduction-icu\n" +
				"        clause: IRDAI/HLT/REG/CIR/151/06/2020 para 7\n" +
				"        see_also: [C.I.1 Note 2]\n" +
				"        applies_to: icu\n" +
				"        effect: no-scaling\n",
			check: func(t *testing.T, p *rules.Pack) {
				if _, ok := p.Rule("proportionate-deduction-icu"); ok {
					t.Error("proportionate-deduction-icu is still there")
				}
				r, ok := p.Rule("no-proportionate-deduction-icu")
				if !ok || r.Effect != rules.EffectNoScaling || r.AppliesTo != "icu" ||
					r.Clause != "IRDAI/HLT/REG/CIR/151/06/2020 para 7" {
					t.Errorf("no-proportionate-deduction-icu = %+v", r)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := rules.ParsePack([]byte(edit(t, src, tt.old, tt.new)))
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, p)
		})
	}
}

func TestParsePackEmpty(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n"} {
		if _, err := rules.ParsePack([]byte(src)); !errors.Is(err, rules.ErrMissingField) {
			t.Errorf("ParsePack(%q) error = %v, want ErrMissingField", src, err)
		}
	}
}

func TestFieldErrorMessage(t *testing.T) {
	src := edit(t, goDigitSource(t), "rate_bp: 500\n", "rate_bp: 5.0\n")
	_, err := rules.ParsePack([]byte(src))
	var fe *rules.FieldError
	if !errors.As(err, &fe) {
		t.Fatalf("error = %v, want a *FieldError", err)
	}
	line := strings.Count(src[:strings.Index(src, "rate_bp: 5.0")], "\n") + 1
	if fe.Line != line {
		t.Errorf("Line = %d, want %d", fe.Line, line)
	}
	want := "rule pack: steps[7].rules[0].rate_bp (line "
	if msg := err.Error(); !strings.HasPrefix(msg, want) || !strings.Contains(msg, "floats are not allowed") {
		t.Errorf("Error() = %q, want it to start %q and name the problem", msg, want)
	}
}

// FuzzParsePack checks that no input makes the parser panic, that every
// error names a field, and that whatever it accepts is complete.
func FuzzParsePack(f *testing.F) {
	src := goDigitSource(f)
	f.Add([]byte(src))
	for _, m := range malformedCases {
		f.Add([]byte(m.apply(f, src)))
	}
	f.Add([]byte("a: &x [*x]\n"))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := rules.ParsePack(b)
		if err != nil {
			var fe *rules.FieldError
			if !errors.As(err, &fe) {
				t.Fatalf("error %v isn't a *FieldError", err)
			}
			return
		}
		if p.Identity().SHA256 != sha256.Sum256(b) {
			t.Fatal("identity SHA-256 isn't the SHA-256 of the input")
		}
		if len(p.Steps) != len(rules.StepNames()) {
			t.Fatalf("accepted a pack with %d steps", len(p.Steps))
		}
	})
}
