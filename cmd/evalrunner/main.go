// Command evalrunner runs the evaluations. Week 2 has the rules engine's
// golden cases (evals/engine):
//
//	go run ./cmd/evalrunner engine
//	go run ./cmd/evalrunner explain evals/engine/golden/01-worked-example.yaml
//
// engine reports each golden case as pass, fail or unchecked, with every
// figure that differs for a failure, and ends with a count. It exits 1
// unless every case is checked and passing, so `go run ./cmd/dev golden`
// is M1's gate. A pass is evals/engine's Evaluate, the same definition the
// golden tests use.
//
// explain prints one claim's lines and adjustments laid out like the worked
// example in docs/plan/architecture.md. It reads any case file, checked or
// not, and compares nothing.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/engine"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

// Exit statuses.
const (
	exitOK    = 0
	exitFail  = 1 // a case fails or is unchecked, or a command failed
	exitUsage = 2
)

const usage = `Usage:
  evalrunner engine                Report every golden case as pass, fail or unchecked
  evalrunner explain <case.yaml>   Lay out one claim's lines and adjustments
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	var err error
	switch cmd := args[0]; {
	case cmd == "engine" && len(args) == 1:
		var ok bool
		if ok, err = engineReport(stdout); err == nil && !ok {
			return exitFail
		}
	case cmd == "explain" && len(args) == 2:
		err = explainFile(stdout, args[1])
	case cmd == "help" || cmd == "-h" || cmd == "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	if err != nil {
		fmt.Fprintln(stderr, "evalrunner:", err)
		return exitFail
	}
	return exitOK
}

// engineReport writes one line per golden case, its verdict and ID, with
// the figures that differ under each failure, then a count. ok is true only
// when every case is checked and passing.
func engineReport(w io.Writer) (ok bool, err error) {
	cases, err := engine.Cases()
	if err != nil {
		return false, err
	}
	reg, err := rulepacks.Registry()
	if err != nil {
		return false, err
	}
	checked, err := engine.Checked()
	if err != nil {
		return false, err
	}

	known := map[string]bool{}
	var nChecked, nPass, nFail, nUnchecked int
	for _, c := range cases {
		known[c.ID] = true
		o := engine.Evaluate(c, reg, checked)
		v := o.Verdict()
		line := fmt.Sprintf("%-9s  %s", v, c.ID)
		if v == engine.VerdictFail && !o.Checked {
			line += " (unchecked)"
		}
		fmt.Fprintln(w, line)
		if o.Checked {
			nChecked++
		}
		switch v {
		case engine.VerdictPass:
			nPass++
		case engine.VerdictUnchecked:
			nUnchecked++
		case engine.VerdictFail:
			nFail++
			if o.Err != nil {
				fmt.Fprintf(w, "           the engine couldn't run it: %v\n", o.Err)
			}
			for _, d := range o.Diffs {
				fmt.Fprintln(w, "           "+d.Describe("case", "engine"))
			}
			for _, d := range o.CheckedDiffs {
				fmt.Fprintln(w, "           "+d.Describe("checked", "case"))
			}
		}
	}

	// Checked rows for a case that doesn't exist mean the CSV and the case
	// files have drifted apart.
	var stray []string
	for id := range checked {
		if !known[id] {
			stray = append(stray, id)
		}
	}
	slices.Sort(stray)
	for _, id := range stray {
		fmt.Fprintf(w, "%-9s  %s: %s has rows for it, but no case file\n", engine.VerdictFail, id, engine.CSVPath)
	}

	summary := fmt.Sprintf("%d cases: %d checked, %d passing", len(cases), nChecked, nPass)
	if nFail > 0 {
		summary += fmt.Sprintf(", %d failing", nFail)
	}
	if nUnchecked > 0 {
		summary += fmt.Sprintf(", %d unchecked", nUnchecked)
	}
	if len(stray) > 0 {
		summary += fmt.Sprintf(", and checked rows for %d unknown cases", len(stray))
	}
	fmt.Fprintln(w, summary)
	return nFail == 0 && nUnchecked == 0 && len(stray) == 0, nil
}

// explainFile reads a case file, adjudicates it under the pack its policy
// period selects, and lays the result out.
func explainFile(w io.Writer, path string) error {
	src, err := os.ReadFile(path) //nolint:gosec // G703: the path is the developer's own argument, a case file to lay out.
	if err != nil {
		return err
	}
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	c, err := engine.ParseCase(id, src)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	reg, err := rulepacks.Registry()
	if err != nil {
		return err
	}
	pack, err := reg.Select(c.Period, c.Facts.Admission)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	res, err := rules.Adjudicate(pack, c.Facts, c.Cover)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return explain(w, c, pack, res)
}
