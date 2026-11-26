// Package extraction holds the synthetic packets' scenario specs, from which
// internal/datagen renders the packets and their truth (week 3). Each spec
// is one YAML file in scenarios/, named by its scenario ID. Golden specs,
// golden-<case>.yaml, render the engine's golden cases with their policy,
// cover and facts unchanged. Seeded specs, seeded-<type>-<nn>.yaml, are
// eight for each of six claim types, written by `datagen scenarios`. Specs
// are written once, reviewed and committed; rendered packets aren't
// committed (ADR 0006: every one is synthetic).
//
// CLAUDE.md rule 9: never change a spec or generated truth by hand to
// improve a number.
package extraction

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// ClaimTypes are the six claim types seeded scenarios come from, as their
// IDs and the split file name them.
var ClaimTypes = []string{"medical", "surgery-implant", "cataract-day-care", "icu-step-down", "accident-ambulance", "long-pharmacy"}

// SeededFlags are the flags a seeded scenario's facts may raise. The rest
// would mean the generator built facts the engine can't take as they are,
// such as a line outside the stay, room days that don't match or a wait
// not served; golden cases cover those.
var SeededFlags = []rules.FlagCode{rules.FlagBillDiscount, rules.FlagICURatioScaling, rules.FlagCoverExceeded}

// CheckSeeded runs the engine on a seeded scenario's facts: it must
// adjudicate them, raise only SeededFlags and pay something.
func CheckSeeded(reg *rules.Registry, period rules.PolicyPeriod, cover rules.Cover, facts rules.Facts) error {
	pack, err := reg.Select(period, facts.Admission)
	if err != nil {
		return err
	}
	res, err := rules.Adjudicate(pack, facts, cover)
	if err != nil {
		return err
	}
	for _, f := range res.Flags {
		if !slices.Contains(SeededFlags, f.Code) {
			return fmt.Errorf("the engine raises %s on line %q", f.Code, f.LineID)
		}
	}
	if res.Payable <= 0 {
		return errors.New("the claim pays nothing")
	}
	return nil
}

//go:embed scenarios/*.yaml
var scenarioFiles embed.FS

// Specs loads every scenario spec, sorted by ID.
func Specs() ([]datagen.Spec, error) {
	sub, err := fs.Sub(scenarioFiles, "scenarios")
	if err != nil {
		return nil, err
	}
	return datagen.LoadSpecs(sub)
}
