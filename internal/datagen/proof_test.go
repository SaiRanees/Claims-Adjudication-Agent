package datagen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
)

// The proof of gopdf the plan asks for before any template (week 3,
// session 3): the rupee sign, Indian and Western digit grouping and a long
// description render, two processes give identical bytes, and PDFium reads
// every string back from the page.

var proofStrings = []string{
	"₹1,23,456.00",
	"Rs. 123,456/-",
	"Disposable surgical drape set with adhesive incise film, sterile, single use, large",
}

func renderProof() ([]byte, error) {
	pdf, err := newPDF()
	if err != nil {
		return nil, err
	}
	pdf.AddPage()
	y := 72.0
	for i, s := range proofStrings {
		font := fontRegular
		if i == 1 {
			font = fontBold
		}
		if err := pdf.SetFont(font, "", 10); err != nil {
			return nil, err
		}
		pdf.SetXY(48, y)
		if err := pdf.Text(s); err != nil {
			return nil, err
		}
		y += 18
	}
	return pdf.GetBytesPdfReturnErr()
}

// childEnv, set to a job and a file path ("proof=/tmp/x"), makes the test
// binary do that job instead of running tests: render, write the bytes to
// the file and exit. It lets a test compare two processes' output without a
// test that skips in every ordinary run.
const childEnv = "DATAGEN_CHILD"

// childJobs are what a child process can be asked to render.
var childJobs = map[string]func() ([]byte, error){
	"proof": renderProof,
}

func TestMain(m *testing.M) {
	if v := os.Getenv(childEnv); v != "" {
		os.Exit(runChildJob(v))
	}
	os.Exit(m.Run())
}

func runChildJob(v string) int {
	job, out, ok := strings.Cut(v, "=")
	render := childJobs[job]
	if !ok || render == nil {
		return 2
	}
	b, err := render()
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		return 1
	}
	if err := os.WriteFile(out, b, 0o600); err != nil { //nolint:gosec // the parent test names a file under its t.TempDir
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		return 1
	}
	return 0
}

// runChild runs a child job in a fresh process of this test binary and
// returns the bytes it wrote.
func runChild(t *testing.T, job string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	cmd := exec.CommandContext(t.Context(), os.Args[0]) //nolint:gosec // the test binary re-runs itself
	cmd.Env = append(os.Environ(), childEnv+"="+job+"="+out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child process: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func TestProofTwoProcesses(t *testing.T) {
	here, err := renderProof()
	if err != nil {
		t.Fatal(err)
	}
	child := runChild(t, "proof")
	if !bytes.Equal(here, child) {
		t.Fatalf("two processes rendered different bytes: %s and %s", sum(here), sum(child))
	}
	again, err := renderProof()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(here, again) {
		t.Fatal("two renderings in one process differ")
	}
	for _, s := range []string{"/CreationDate", "/ID", "/Encrypt"} {
		if bytes.Contains(here, []byte(s)) {
			t.Errorf("the PDF has %s", s)
		}
	}
	t.Logf("proof PDF: %d bytes, sha256 %s", len(here), sum(here))
}

func newTestPDFium(t *testing.T) *documents.PDFium {
	t.Helper()
	p, err := documents.NewPDFium(documents.DefaultPDFiumConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestProofPDFiumReadsBack(t *testing.T) {
	b, err := renderProof()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := newTestPDFium(t).PageTexts(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	if len(texts) != 1 {
		t.Fatalf("got %d pages, want 1", len(texts))
	}
	for _, s := range proofStrings {
		if !strings.Contains(texts[0], s) {
			t.Errorf("PDFium didn't read %q back; page text:\n%q", s, texts[0])
		}
	}
}
