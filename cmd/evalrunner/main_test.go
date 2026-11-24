package main

import (
	"bytes"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/engine"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules/money"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

func TestRupees(t *testing.T) {
	tests := []struct {
		paise money.Paise
		want  string
	}{
		{0, "0.00"},
		{5, "0.05"},
		{100, "1.00"},
		{99_999, "999.99"},
		{100_000, "1,000.00"},
		{612_500, "6,125.00"},
		{17_400_000, "1,74,000.00"},
		{11_637_500, "1,16,375.00"},
		{1_000_000_000, "1,00,00,000.00"},
		{-612_500, "−6,125.00"},
		{math.MaxInt64, "92,23,37,20,36,85,47,758.07"},
		{math.MinInt64, "−92,23,37,20,36,85,47,758.08"},
	}
	for _, tt := range tests {
		if got := rupees(tt.paise); got != tt.want {
			t.Errorf("rupees(%d) = %q, want %q", tt.paise, got, tt.want)
		}
	}
}

func TestPercentText(t *testing.T) {
	tests := []struct {
		bp   money.BasisPoints
		want string
	}{
		{0, "0%"}, {1, "0.01%"}, {500, "5%"}, {1234, "12.34%"}, {6250, "62.5%"}, {10_000, "100%"},
	}
	for _, tt := range tests {
		if got := percentText(tt.bp); got != tt.want {
			t.Errorf("percentText(%d) = %q, want %q", tt.bp, got, tt.want)
		}
	}
}

func TestRunUsage(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{nil, exitUsage},
		{[]string{"nonsense"}, exitUsage},
		{[]string{"engine", "extra"}, exitUsage},
		{[]string{"explain"}, exitUsage},
		{[]string{"explain", "a.yaml", "b.yaml"}, exitUsage},
		{[]string{"help"}, exitOK},
		{[]string{"explain", filepath.Join("testdata", "no-such-case.yaml")}, exitFail},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if got := run(tt.args, &out, &errOut); got != tt.want {
			t.Errorf("run(%q) = %d, want %d (stderr %q)", tt.args, got, tt.want, errOut.String())
		}
	}
}

// TestEngineReport checks the report against engine.Evaluate, the
// definition of a pass the golden tests share: one line per case with its
// verdict, a count that agrees, and the exit status the gate reads.
func TestEngineReport(t *testing.T) {
	cases, err := engine.Cases()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := rulepacks.Registry()
	if err != nil {
		t.Fatal(err)
	}
	checked, err := engine.Checked()
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	status := run([]string{"engine"}, &out, &errOut)
	report := out.String()

	nChecked, nPass := 0, 0
	for _, c := range cases {
		o := engine.Evaluate(c, reg, checked)
		line := regexp.MustCompile(`(?m)^` + string(o.Verdict()) + ` +` + regexp.QuoteMeta(c.ID) + `( \(unchecked\))?$`)
		if !line.MatchString(report) {
			t.Errorf("the report has no %s line for %s:\n%s", o.Verdict(), c.ID, report)
		}
		if o.Checked {
			nChecked++
		}
		if o.Verdict() == engine.VerdictPass {
			nPass++
		}
	}
	lines := strings.Split(strings.TrimSpace(report), "\n")
	want := fmt.Sprintf("%d cases: %d checked, %d passing", len(cases), nChecked, nPass)
	if summary := lines[len(lines)-1]; !strings.HasPrefix(summary, want) {
		t.Errorf("the report ends %q, want it to start %q", summary, want)
	}
	wantStatus := exitFail
	if nPass == len(cases) && len(checked) == nChecked {
		wantStatus = exitOK
	}
	if status != wantStatus {
		t.Errorf("exit status %d with %d of %d cases passing, want %d", status, nPass, len(cases), wantStatus)
	}
}

// TestExplainWorkedExample checks explain against the worked example in
// docs/plan/architecture.md, which golden case 01 is.
func TestExplainWorkedExample(t *testing.T) {
	var out, errOut bytes.Buffer
	path := filepath.Join("..", "..", "evals", "engine", "golden", "01-worked-example.yaml")
	if status := run([]string{"explain", path}, &out, &errOut); status != exitOK {
		t.Fatalf("explain exited %d: %s", status, errOut.String())
	}
	got := out.String()
	for _, want := range []string{
		"Case 01-worked-example",
		"Sum insured ₹5,00,000.00.",
		"| L01 room-rent, 4 × 8,000.00 ",
		"Capped at ₹5,000.00 a day for 4 days (daily-cap-room, C.I.1(i)): −12,000.00",
		"= 62.5% (proportionate-deduction-room, C.I.1 Note 2): −37,500.00",
		"| Exempt from scaling ",
		"(annexure-a-list-ii, Annexure A List II): −2,000.00",
		"| **Subtotal** ", "**1,74,000.00**", "**1,22,500.00**",
		"| Co-payment, 5% ", "−6,125.00",
		"**1,16,375.00**",
		"Flags: none.",
		"Ledger: ₹1,16,375.00 paid.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("explain's output lacks %q:\n%s", want, got)
		}
	}
}

// TestExplainEveryCase lays out every golden case, so a step or a flag the
// layout can't add up fails here rather than in a reviewer's terminal.
func TestExplainEveryCase(t *testing.T) {
	cases, err := engine.Cases()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		var out, errOut bytes.Buffer
		path := filepath.Join("..", "..", "evals", "engine", "golden", c.ID+".yaml")
		if status := run([]string{"explain", path}, &out, &errOut); status != exitOK {
			t.Errorf("explain %s exited %d: %s", c.ID, status, errOut.String())
		}
	}
}
