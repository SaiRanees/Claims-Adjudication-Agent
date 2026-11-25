// Package extraction holds the synthetic packets' scenario specs, from which
// internal/datagen renders the packets and their truth (week 3). Each spec
// is one YAML file in scenarios/, named by its scenario ID. Golden specs,
// golden-<case>.yaml, render the engine's golden cases with their policy,
// cover and facts unchanged. Specs are written once, reviewed and
// committed; rendered packets aren't committed (ADR 0006: every one is
// synthetic).
//
// CLAUDE.md rule 9: never change a spec or generated truth by hand to
// improve a number.
package extraction

import (
	"embed"
	"io/fs"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
)

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
