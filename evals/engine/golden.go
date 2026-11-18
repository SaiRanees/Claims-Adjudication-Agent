// Package engine holds the rules engine's golden cases and runs them. Each
// case is one YAML file in golden/: a claim's policy, cover and facts, the
// result a person worked out by hand, and that arithmetic in comments. Every
// case is synthetic (ADR 0006).
//
// The facts half has the shape of a scenario spec, so M2's generator can
// reuse it. A person checks each case's figures in a spreadsheet and exports
// them to golden-cases.csv; a case with no rows there is unchecked.
package engine

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
)

//go:embed golden/*.yaml
var goldenFiles embed.FS

// Case is one golden case.
type Case struct {
	// ID is the file's name without .yaml.
	ID     string
	Period rules.PolicyPeriod
	Cover  rules.Cover
	Facts  rules.Facts
	Want   Expected
}

// Expected is the result a person worked out by hand.
type Expected struct {
	Lines        []WantLine
	Adjustments  []WantAdjustment
	Flags        []WantFlag
	ClaimPayable money.Paise
	Ledger       rules.Ledger
}

// WantLine is one line's expected payable.
type WantLine struct {
	ID      string
	Payable money.Paise
}

// WantAdjustment is one expected non-zero adjustment.
type WantAdjustment struct {
	Line   string
	Rule   string
	Amount money.Paise
}

// WantFlag is one expected flag.
type WantFlag struct {
	Code      rules.FlagCode
	Line      string
	Figure    money.Paise
	HasFigure bool
}

// Cases loads every golden case, sorted by ID.
func Cases() ([]Case, error) {
	paths, err := fs.Glob(goldenFiles, "golden/*.yaml")
	if err != nil {
		return nil, err
	}
	cases := make([]Case, 0, len(paths))
	for _, p := range paths {
		src, err := goldenFiles.ReadFile(p)
		if err != nil {
			return nil, err
		}
		c, err := parseCase(strings.TrimSuffix(path.Base(p), ".yaml"), src)
		if err != nil {
			return nil, fmt.Errorf("golden case %s: %w", p, err)
		}
		cases = append(cases, c)
	}
	slices.SortFunc(cases, func(a, b Case) int { return strings.Compare(a.ID, b.ID) })
	return cases, nil
}

// The file's shape. Money is integer paise in fields ending _paise; dates
// are YYYY-MM-DD.
type caseSpec struct {
	Policy struct {
		WordingUIN  string `yaml:"wording_uin"`
		PeriodStart string `yaml:"period_start"`
		PeriodEnd   string `yaml:"period_end"`
	} `yaml:"policy"`
	Cover struct {
		Tranches []struct {
			SumInsured      int64  `yaml:"sum_insured_paise"`
			ContinuousSince string `yaml:"continuous_since"`
		} `yaml:"tranches"`
		CumulativeBonus     int64   `yaml:"cumulative_bonus_paise"`
		PaidThisYear        int64   `yaml:"paid_this_year_paise"`
		CataractUsed        eyeSpec `yaml:"cataract_used_paise"`
		ModernTreatmentUsed int64   `yaml:"modern_treatment_used_paise"`
	} `yaml:"cover"`
	Facts struct {
		Admission string `yaml:"admission"`
		Discharge string `yaml:"discharge"`
		Segments  []struct {
			From string `yaml:"from"`
			To   string `yaml:"to"`
			Kind string `yaml:"kind"`
		} `yaml:"segments"`
		DifferentialBilling string `yaml:"differential_billing"`
		BillDiscount        int64  `yaml:"bill_discount_paise"`
		Condition           struct {
			ListedGroup string `yaml:"listed_group"`
			PreExisting bool   `yaml:"pre_existing"`
			Accident    bool   `yaml:"accident"`
			Onset       string `yaml:"onset"`
		} `yaml:"condition"`
		Lines []struct {
			ID          string `yaml:"id"`
			Category    string `yaml:"category"`
			ServiceDate string `yaml:"service_date"`
			Quantity    int64  `yaml:"quantity"`
			UnitPrice   int64  `yaml:"unit_price_paise"`
			Amount      int64  `yaml:"amount_paise"`
			ItemList    string `yaml:"item_list"`
			PackageRate bool   `yaml:"package_rate"`
			Treatment   struct {
				Kind string `yaml:"kind"`
				Eye  string `yaml:"eye"`
			} `yaml:"treatment"`
		} `yaml:"lines"`
	} `yaml:"facts"`
	Expected struct {
		Lines []struct {
			ID      string `yaml:"id"`
			Payable int64  `yaml:"payable_paise"`
		} `yaml:"lines"`
		Adjustments []struct {
			Line   string `yaml:"line"`
			Rule   string `yaml:"rule"`
			Amount int64  `yaml:"paise"`
		} `yaml:"adjustments"`
		Flags []struct {
			Code   string `yaml:"code"`
			Line   string `yaml:"line"`
			Figure *int64 `yaml:"paise"`
		} `yaml:"flags"`
		ClaimPayable int64 `yaml:"claim_payable_paise"`
		Ledger       struct {
			Paid                int64   `yaml:"paid_paise"`
			CataractUsed        eyeSpec `yaml:"cataract_used_paise"`
			ModernTreatmentUsed int64   `yaml:"modern_treatment_used_paise"`
		} `yaml:"ledger"`
	} `yaml:"expected"`
}

type eyeSpec struct {
	Left  int64 `yaml:"left"`
	Right int64 `yaml:"right"`
}

func (e eyeSpec) amounts() rules.EyeAmounts {
	return rules.EyeAmounts{Left: money.Paise(e.Left), Right: money.Paise(e.Right)}
}

// dates parses YYYY-MM-DD strings, leaving an empty one as the zero Date.
type dates struct{ err error }

func (d *dates) parse(field, s string) rules.Date {
	if s == "" || d.err != nil {
		return rules.Date{}
	}
	v, err := rules.ParseDate(s)
	if err != nil {
		d.err = fmt.Errorf("%s: %w", field, err)
	}
	return v
}

func parseCase(id string, src []byte) (Case, error) {
	var s caseSpec
	dec := yaml.NewDecoder(bytes.NewReader(src))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Case{}, err
	}
	var d dates
	c := Case{ID: id}

	c.Period = rules.PolicyPeriod{
		WordingUIN: s.Policy.WordingUIN,
		Start:      d.parse("policy.period_start", s.Policy.PeriodStart),
		End:        d.parse("policy.period_end", s.Policy.PeriodEnd),
	}

	for i, t := range s.Cover.Tranches {
		c.Cover.Tranches = append(c.Cover.Tranches, rules.Tranche{
			SumInsured:      money.Paise(t.SumInsured),
			ContinuousSince: d.parse(fmt.Sprintf("cover.tranches[%d].continuous_since", i), t.ContinuousSince),
		})
	}
	c.Cover.CumulativeBonus = money.Paise(s.Cover.CumulativeBonus)
	c.Cover.PaidThisYear = money.Paise(s.Cover.PaidThisYear)
	c.Cover.CataractUsed = s.Cover.CataractUsed.amounts()
	c.Cover.ModernTreatmentUsed = money.Paise(s.Cover.ModernTreatmentUsed)

	f := &c.Facts
	f.Admission = d.parse("facts.admission", s.Facts.Admission)
	f.Discharge = d.parse("facts.discharge", s.Facts.Discharge)
	for i, seg := range s.Facts.Segments {
		f.Segments = append(f.Segments, rules.Segment{
			From: d.parse(fmt.Sprintf("facts.segments[%d].from", i), seg.From),
			To:   d.parse(fmt.Sprintf("facts.segments[%d].to", i), seg.To),
			Kind: rules.SegmentKind(seg.Kind),
		})
	}
	f.DifferentialBilling = rules.Answer(s.Facts.DifferentialBilling)
	f.BillDiscount = money.Paise(s.Facts.BillDiscount)
	f.Condition = rules.Condition{
		ListedGroup: s.Facts.Condition.ListedGroup,
		PreExisting: s.Facts.Condition.PreExisting,
		Accident:    s.Facts.Condition.Accident,
		Onset:       d.parse("facts.condition.onset", s.Facts.Condition.Onset),
	}
	for i, l := range s.Facts.Lines {
		f.Lines = append(f.Lines, rules.Line{
			ID:          l.ID,
			Category:    rules.Category(l.Category),
			ServiceDate: d.parse(fmt.Sprintf("facts.lines[%d].service_date", i), l.ServiceDate),
			Quantity:    l.Quantity,
			UnitPrice:   money.Paise(l.UnitPrice),
			Amount:      money.Paise(l.Amount),
			ItemList:    l.ItemList,
			PackageRate: l.PackageRate,
			Treatment:   rules.Treatment{Kind: rules.TreatmentKind(l.Treatment.Kind), Eye: rules.Eye(l.Treatment.Eye)},
		})
	}

	w := &c.Want
	for _, l := range s.Expected.Lines {
		w.Lines = append(w.Lines, WantLine{ID: l.ID, Payable: money.Paise(l.Payable)})
	}
	for _, a := range s.Expected.Adjustments {
		w.Adjustments = append(w.Adjustments, WantAdjustment{Line: a.Line, Rule: a.Rule, Amount: money.Paise(a.Amount)})
	}
	for _, fl := range s.Expected.Flags {
		wf := WantFlag{Code: rules.FlagCode(fl.Code), Line: fl.Line}
		if fl.Figure != nil {
			wf.Figure, wf.HasFigure = money.Paise(*fl.Figure), true
		}
		w.Flags = append(w.Flags, wf)
	}
	w.ClaimPayable = money.Paise(s.Expected.ClaimPayable)
	w.Ledger = rules.Ledger{
		Paid:                money.Paise(s.Expected.Ledger.Paid),
		CataractUsed:        s.Expected.Ledger.CataractUsed.amounts(),
		ModernTreatmentUsed: money.Paise(s.Expected.Ledger.ModernTreatmentUsed),
	}
	return c, d.err
}
