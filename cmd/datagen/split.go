package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/extraction"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// defaultSplit is the split file, relative to the repository's root.
const defaultSplit = "evals/extraction/split.csv"

// splitScenarios appends to the split file every scenario it doesn't list
// yet, and never changes a line already there: a scenario stays in its
// half for good. The new scenarios are grouped by claim type, in
// extraction.ClaimTypes' order. Within a type they're sorted by the
// SHA-256 of their IDs and alternate between the halves, starting with the
// half the type has fewer of, dev on a tie, so each type's halves never
// differ by more than one.
func splitScenarios(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specsDir := fs.String("specs", defaultSpecs, "directory of scenario specs")
	file := fs.String("file", defaultSplit, "the split file to append to")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprint(stderr, usage)
		return errUsage
	}
	specs, err := datagen.LoadSpecs(os.DirFS(*specsDir))
	if err != nil {
		return err
	}
	src, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	entries, err := extraction.ParseSplit(src)
	if err != nil {
		return err
	}
	added, err := newSplitEntries(specs, entries)
	if err != nil {
		return err
	}
	var b strings.Builder
	if len(src) > 0 && src[len(src)-1] != '\n' {
		b.WriteString("\n")
	}
	for _, e := range added {
		b.WriteString(e.String() + "\n")
	}
	f, err := os.OpenFile(*file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "appended %d scenarios to %s; %d were already there\n", len(added), *file, len(entries))
	return nil
}

// newSplitEntries splits the scenarios the entries don't list.
func newSplitEntries(specs []datagen.Spec, entries []extraction.SplitEntry) ([]extraction.SplitEntry, error) {
	listed := map[string]bool{}
	count := map[string]map[string]int{}
	for _, ct := range extraction.ClaimTypes {
		count[ct] = map[string]int{}
	}
	for _, e := range entries {
		if listed[e.Scenario] {
			return nil, fmt.Errorf("split: %s is listed twice", e.Scenario)
		}
		listed[e.Scenario] = true
		count[e.ClaimType][e.Half]++
	}
	byType := map[string][]string{}
	for _, s := range specs {
		if listed[s.ID] {
			continue
		}
		ct, err := claimTypeOf(s)
		if err != nil {
			return nil, err
		}
		byType[ct] = append(byType[ct], s.ID)
	}
	var added []extraction.SplitEntry
	for _, ct := range extraction.ClaimTypes {
		ids := byType[ct]
		slices.SortFunc(ids, func(a, b string) int { return strings.Compare(idHash(a), idHash(b)) })
		half := extraction.HalfDev
		if count[ct][extraction.HalfTest] < count[ct][extraction.HalfDev] {
			half = extraction.HalfTest
		}
		for _, id := range ids {
			added = append(added, extraction.SplitEntry{Scenario: id, ClaimType: ct, Half: half})
			if half == extraction.HalfDev {
				half = extraction.HalfTest
			} else {
				half = extraction.HalfDev
			}
		}
	}
	return added, nil
}

func idHash(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// claimTypeOf is a seeded scenario's claim type, from its ID, or the type a
// golden case is closest to.
func claimTypeOf(s datagen.Spec) (string, error) {
	if s.GoldenCase == "" {
		for _, ct := range extraction.ClaimTypes {
			if strings.HasPrefix(s.ID, "seeded-"+ct+"-") {
				return ct, nil
			}
		}
		return "", fmt.Errorf("split: %s names no claim type", s.ID)
	}
	return closestType(s)
}

// closestType is the claim type a golden case is closest to, by the first
// of these its facts show: a line tied to cataract surgery, an ICU
// segment, an accident or a road ambulance, an implant or a procedure, a
// stay of ten nights or more or a bill mostly of pharmacy lines; or else a
// medical admission.
func closestType(s datagen.Spec) (string, error) {
	_, _, f, err := s.Engine()
	if err != nil {
		return "", err
	}
	has := func(match func(rules.Line) bool) bool { return slices.ContainsFunc(f.Lines, match) }
	pharmacy := 0
	for _, l := range f.Lines {
		if l.Category == rules.CategoryPharmacyConsumables {
			pharmacy++
		}
	}
	switch {
	case has(func(l rules.Line) bool { return l.Treatment.Kind == rules.TreatmentCataract }):
		return "cataract-day-care", nil
	case slices.ContainsFunc(f.Segments, func(sg rules.Segment) bool { return sg.Kind == rules.SegmentICU }):
		return "icu-step-down", nil
	case f.Condition.Accident || has(func(l rules.Line) bool { return l.Category == rules.CategoryRoadAmbulance }):
		return "accident-ambulance", nil
	case s.Clinical.Procedure != "" || has(func(l rules.Line) bool { return l.Category == rules.CategoryImplantsDevices }):
		return "surgery-implant", nil
	case f.Admission.DaysUntil(f.Discharge) >= 10 || 2*pharmacy > len(f.Lines):
		return "long-pharmacy", nil
	}
	return "medical", nil
}
