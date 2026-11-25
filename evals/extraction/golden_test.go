package extraction

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/engine"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
	"github.com/SaiRanees/Claims-Adjudication-Agent/rulepacks"
)

// The tests in week 3, session 3's "Done when": the 52 golden packets hold
// their cases' facts unchanged, the engine gets each case's checked figures
// from its packet's truth, every packet passes the self-check, and two
// processes render identical bytes.

// childEnv, set to a file path, makes the test binary render every packet,
// write a manifest of their hashes to that file and exit, instead of
// running tests.
const childEnv = "EXTRACTION_RENDER_MANIFEST"

func TestMain(m *testing.M) {
	if out := os.Getenv(childEnv); out != "" {
		manifest, err := renderManifest()
		if err == nil {
			err = os.WriteFile(out, manifest, 0o600) //nolint:gosec // the parent test names a file under its t.TempDir
		}
		if err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// renderManifest renders every spec and lists each file's and truth's
// SHA-256, one per line.
func renderManifest() ([]byte, error) {
	specs, err := Specs()
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for _, s := range specs {
		p, err := datagen.Render(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.ID, err)
		}
		for _, f := range p.Files {
			fmt.Fprintf(&b, "%s/%s %s\n", s.ID, f.Name, sha(f.Data))
		}
		truth, err := datagen.TruthJSON(p.Truth)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "%s/truth.json %s\n", s.ID, sha(truth))
	}
	return b.Bytes(), nil
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func goldenSpecs(t *testing.T) []datagen.Spec {
	t.Helper()
	specs, err := Specs()
	if err != nil {
		t.Fatal(err)
	}
	var golden []datagen.Spec
	for _, s := range specs {
		if s.GoldenCase != "" {
			golden = append(golden, s)
		}
	}
	return golden
}

func TestGoldenSpecsHoldTheirCases(t *testing.T) {
	cases, err := engine.Cases()
	if err != nil {
		t.Fatal(err)
	}
	specs := goldenSpecs(t)
	if len(specs) != len(cases) || len(cases) != 52 {
		t.Fatalf("%d golden specs for %d golden cases, want 52 of each", len(specs), len(cases))
	}
	byCase := map[string]datagen.Spec{}
	for _, s := range specs {
		if s.ID != "golden-"+s.GoldenCase {
			t.Errorf("spec %s renders case %s; its name should be golden-%s", s.ID, s.GoldenCase, s.GoldenCase)
		}
		byCase[s.GoldenCase] = s
	}
	for _, c := range cases {
		s, ok := byCase[c.ID]
		if !ok {
			t.Errorf("case %s has no spec", c.ID)
			continue
		}
		period, cover, facts, err := s.Engine()
		if err != nil {
			t.Errorf("%s: %v", s.ID, err)
			continue
		}
		if !reflect.DeepEqual(period, c.Period) || !reflect.DeepEqual(cover, c.Cover) || !reflect.DeepEqual(facts, c.Facts) {
			t.Errorf("%s: policy, cover or facts differ from case %s", s.ID, c.ID)
		}
	}
}

// TestGoldenPacketsPayTheCheckedFigures runs the engine on each golden
// packet's truth.json, decoded as M3 will read it, and compares the result
// with the case's checked rows in golden-cases.csv: the claim's payable,
// and every line payable, adjustment and flag.
func TestGoldenPacketsPayTheCheckedFigures(t *testing.T) {
	reg, err := rulepacks.Registry()
	if err != nil {
		t.Fatal(err)
	}
	checked, err := engine.Checked()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range goldenSpecs(t) {
		t.Run(s.ID, func(t *testing.T) {
			rows := checked[s.GoldenCase]
			if len(rows) == 0 {
				t.Fatalf("case %s has no checked figures", s.GoldenCase)
			}
			p, err := datagen.Render(s)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := datagen.TruthJSON(p.Truth)
			if err != nil {
				t.Fatal(err)
			}
			var truth datagen.Truth
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&truth); err != nil {
				t.Fatal(err)
			}
			e := truth.Engine
			period, cover, facts, err := datagen.EngineInput(e.Policy, e.Cover, e.Facts)
			if err != nil {
				t.Fatal(err)
			}
			pack, err := reg.Select(period, facts.Admission)
			if err != nil {
				t.Fatal(err)
			}
			res, err := rules.Adjudicate(pack, facts, cover)
			if err != nil {
				t.Fatal(err)
			}
			if want := checkedPayable(t, rows); int64(res.Payable) != want {
				t.Errorf("payable %d paise, checked %d", res.Payable, want)
			}
			if got := engine.ResultRows(res); !slices.Equal(got, rows) {
				t.Errorf("the engine's rows differ from the checked rows:\n got %v\nwant %v", got, rows)
			}
		})
	}
}

func checkedPayable(t *testing.T, rows []engine.Row) int64 {
	t.Helper()
	for _, r := range rows {
		if r.Kind == engine.KindClaimPayable {
			return r.Paise
		}
	}
	t.Fatal("no claim_payable row")
	return 0
}

func TestGoldenPacketsSelfCheck(t *testing.T) {
	pdfium, err := documents.NewPDFium(documents.DefaultPDFiumConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pdfium.Close() })
	for _, s := range goldenSpecs(t) {
		t.Run(s.ID, func(t *testing.T) {
			p, err := datagen.Render(s)
			if err != nil {
				t.Fatal(err)
			}
			if err := datagen.SelfCheck(t.Context(), pdfium, p); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestPacketsRenderIdenticallyInTwoProcesses renders every packet here and
// in a fresh process, and compares every file's and truth's SHA-256.
func TestPacketsRenderIdenticallyInTwoProcesses(t *testing.T) {
	here, err := renderManifest()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "manifest")
	cmd := exec.CommandContext(t.Context(), os.Args[0]) //nolint:gosec // the test binary re-runs itself
	cmd.Env = append(os.Environ(), childEnv+"="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process: %v\n%s", err, b)
	}
	there, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(here, there) {
		a, b := strings.Split(string(here), "\n"), strings.Split(string(there), "\n")
		for i := range min(len(a), len(b)) {
			if a[i] != b[i] {
				t.Errorf("differs: %q here, %q in the child", a[i], b[i])
			}
		}
		t.Fatal("two processes rendered different packets")
	}
	if n := strings.Count(string(here), "/truth.json "); n < 52 {
		t.Errorf("rendered %d packets, want at least 52", n)
	}
}
