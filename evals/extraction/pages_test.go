package extraction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	jobs "github.com/SaiRanees/Claims-Adjudication-Agent/internal/extraction"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/rules"
)

// cassetteDir holds the dev half's recorded model answers, one file per
// request as <job>/<hash>.json. They change only through a record run
// (CLAUDE.md, rule 9), and no test-half request ever has one.
const cassetteDir = "cassettes"

// pendingClassification is how many dev-half pages have no classification
// cassette yet, because the free tier's daily limit stopped the record run.
// The replay test needs exactly this many misses: more means a request's
// hash moved, fewer that this count is out of date. It reaches 0 when the
// recording is complete (go run ./cmd/dev record-pages).
const pendingClassification = 267

// devPacket is one dev-half packet, rendered and prepared.
type devPacket struct {
	id    string
	truth datagen.Truth
	// pages in claim order: the packet's files in order, pages ascending.
	pages []jobs.PageInput
}

var (
	devOnce    sync.Once
	devPackets []devPacket
	devErr     error
)

// preparedDevHalf renders every dev-half packet and prepares its pages, once
// per test binary. It never renders a test-half packet.
func preparedDevHalf(t *testing.T) []devPacket {
	t.Helper()
	devOnce.Do(func() { devPackets, devErr = prepareDevHalf(context.Background()) })
	if devErr != nil {
		t.Fatal(devErr)
	}
	return devPackets
}

func prepareDevHalf(ctx context.Context) ([]devPacket, error) {
	split, err := Split()
	if err != nil {
		return nil, err
	}
	dev := map[string]bool{}
	for _, e := range split {
		if e.Half == HalfDev {
			dev[e.Scenario] = true
		}
	}
	specs, err := Specs()
	if err != nil {
		return nil, err
	}
	pdfium, err := documents.NewPDFium(documents.DefaultPDFiumConfig)
	if err != nil {
		return nil, err
	}
	defer func() { _ = pdfium.Close() }()
	var out []devPacket
	for _, s := range specs {
		if !dev[s.ID] {
			continue
		}
		p, err := datagen.Render(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.ID, err)
		}
		pk := devPacket{id: s.ID, truth: p.Truth}
		for _, f := range p.Files {
			sum := sha256.Sum256(f.Data)
			fileSHA := hex.EncodeToString(sum[:])
			pages, err := documents.Prepare(ctx, pdfium, f.Data)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", s.ID, f.Name, err)
			}
			for _, pg := range pages {
				pk.pages = append(pk.pages, jobs.PageInput{
					ID:         documents.PageID{File: f.Name, Page: pg.Number},
					FileSHA256: fileSHA,
					Page:       pg,
				})
			}
		}
		out = append(out, pk)
	}
	if len(out) != len(dev) {
		return nil, fmt.Errorf("%d dev-half scenarios in the split, %d specs", len(dev), len(out))
	}
	return out, nil
}

// pageType is the truth's document type as a page type.
func pageType(d datagen.DocumentType) jobs.PageType {
	return jobs.PageType(strings.ReplaceAll(string(d), "-", "_"))
}

// claimContext is what the checklist needs, from the truth.
func claimContext(tr datagen.Truth) (jobs.ClaimContext, error) {
	c := jobs.ClaimContext{Surgery: tr.Claim.Procedure != ""}
	for _, l := range tr.Engine.Facts.Lines {
		if rules.Category(l.Category) == rules.CategoryImplantsDevices {
			c.ImplantBilled = true
		}
	}
	for _, d := range tr.Other {
		for _, f := range d.Fields {
			if d.Type == datagen.DocClaimForm && f.Name == "claimed_amount" {
				v, err := strconv.ParseInt(f.Value, 10, 64)
				if err != nil {
					return c, fmt.Errorf("claimed amount %q: %w", f.Value, err)
				}
				c.ClaimedPaise = v
			}
		}
	}
	if c.ClaimedPaise == 0 {
		// A packet without a claim form claims its bills' net total.
		for _, b := range tr.Bills {
			c.ClaimedPaise += b.Totals.Net.Paise
		}
	}
	return c, nil
}

// packetScore compares one packet's classification, grouping and
// checklist with its truth.
type packetScore struct {
	pages, typesRight              int
	truthParts, partsExact         int
	checklistItems, checklistRight int
	mistyped                       []string
}

func scorePacket(pk devPacket, classified []jobs.ClassifiedPage, parts []jobs.Part) (packetScore, error) {
	var s packetScore
	truthType := map[documents.PageID]jobs.PageType{}
	var truthParts []jobs.Part
	for _, f := range pk.truth.Files {
		for _, p := range f.Pages {
			truthType[documents.PageID{File: f.Name, Page: p.Page}] = pageType(p.Type)
		}
		for _, p := range f.Parts {
			truthParts = append(truthParts, jobs.Part{File: f.Name, Type: pageType(p.Type), FirstPage: p.FirstPage, LastPage: p.LastPage})
		}
	}
	for _, c := range classified {
		want, ok := truthType[c.ID]
		if !ok {
			return s, fmt.Errorf("%s: no truth for %s", pk.id, c.ID)
		}
		s.pages++
		if c.Type == want {
			s.typesRight++
		} else {
			s.mistyped = append(s.mistyped, fmt.Sprintf("%s %s: %s, truth %s", pk.id, c.ID, c.Type, want))
		}
	}
	s.truthParts = len(truthParts)
	for _, tp := range truthParts {
		if slices.ContainsFunc(parts, func(p jobs.Part) bool {
			return p.File == tp.File && p.Type == tp.Type && p.FirstPage == tp.FirstPage && p.LastPage == tp.LastPage
		}) {
			s.partsExact++
		}
	}
	ctx, err := claimContext(pk.truth)
	if err != nil {
		return s, fmt.Errorf("%s: %w", pk.id, err)
	}
	var truthDocs []jobs.PageType
	for _, d := range pk.truth.Documents {
		truthDocs = append(truthDocs, pageType(d))
	}
	want := jobs.Checklist(truthDocs, ctx)
	got := jobs.Checklist(jobs.PartTypes(parts), ctx)
	s.checklistItems = len(want)
	for i := range want {
		if got[i] == want[i] {
			s.checklistRight++
		}
	}
	return s, nil
}

// classifyPackets classifies, groups and checks every dev-half page
// through client. A packet with a cassette miss is counted and skipped; a
// page whose answer failed (a stop, or output that doesn't fit the schema)
// is counted and grouped as other, as week 4's workflow will.
func classifyPackets(t *testing.T, client *llm.Client) (misses int, hashes map[string]bool) {
	t.Helper()
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	hashes = map[string]bool{}
	var total packetScore
	var failed, packetsScored, repeats, modelHits, noText int
	seen := map[jobs.PageType]bool{}
	for _, pk := range preparedDevHalf(t) {
		var classified []jobs.ClassifiedPage
		var fps []documents.PageFingerprint
		complete := true
		for _, in := range pk.pages {
			req, err := jobs.ClassificationRequest(cfg, in, 0)
			if err != nil {
				t.Fatal(err)
			}
			h, err := llm.RequestHash(req)
			if err != nil {
				t.Fatal(err)
			}
			hashes[h] = true
			c, err := jobs.Classify(t.Context(), client, cfg, in)
			switch {
			case errors.Is(err, llm.ErrCassetteMiss):
				misses++
				complete = false
				continue
			case err != nil:
				failed++
				t.Logf("%s %s: %v", pk.id, in.ID, err)
				c = jobs.ClassifiedPage{ID: in.ID, Type: jobs.PageOther, HasTextLayer: in.Page.HasTextLayer}
			}
			// The synthetic packets carry no instruction-like text, so a
			// hit from the code's scan is a phrase that ordinary text uses.
			for _, h := range c.CodeInstructionLike {
				t.Errorf("%s %s: the scan matched %q in %q", pk.id, in.ID, h.Phrase, h.Snippet)
			}
			modelHits += len(c.ModelInstructionLike)
			if !c.HasTextLayer {
				noText++
			}
			classified = append(classified, c)
			fps = append(fps, documents.PageFingerprint{ID: in.ID, Fingerprint: in.Page.Fingerprint})
		}
		for _, d := range pk.truth.Documents {
			seen[pageType(d)] = true
		}
		if !complete {
			continue
		}
		rep, err := documents.Repeats(fps)
		if err != nil {
			t.Fatal(err)
		}
		for i, r := range rep {
			classified[i].RepeatOf = r
			if r != nil {
				repeats++
			}
		}
		parts, err := jobs.Group(classified)
		if err != nil {
			t.Fatalf("%s: %v", pk.id, err)
		}
		s, err := scorePacket(pk, classified, parts)
		if err != nil {
			t.Fatal(err)
		}
		packetsScored++
		total.pages += s.pages
		total.typesRight += s.typesRight
		total.truthParts += s.truthParts
		total.partsExact += s.partsExact
		total.checklistItems += s.checklistItems
		total.checklistRight += s.checklistRight
		total.mistyped = append(total.mistyped, s.mistyped...)
	}
	var never []string
	for _, pt := range jobs.PageTypes() {
		if !seen[pt] {
			never = append(never, string(pt))
		}
	}
	// A first look only: session 8 reports these as metrics, and week 4
	// sets the baseline.
	t.Logf("%d packets scored, %d pages: types %d/%d right, parts %d/%d exact, checklist %d/%d right",
		packetsScored, total.pages, total.typesRight, total.pages, total.partsExact, total.truthParts,
		total.checklistRight, total.checklistItems)
	t.Logf("%d failed answers, %d cassette misses, %d repeated pages, %d pages without a text layer, %d model instruction-like snippets",
		failed, misses, repeats, noText, modelHits)
	t.Logf("types the dev half never shows: %s", strings.Join(never, ", "))
	for _, m := range total.mistyped {
		t.Logf("mistyped: %s", m)
	}
	return misses, hashes
}

// TestClassificationReplay replays page classification on every dev-half
// page with no key, then groups each packet's pages and checks its
// checklist. It fails on any miss beyond pendingClassification, and on a
// cassette that isn't the answer to a dev-half request.
func TestClassificationReplay(t *testing.T) {
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := llm.NewClient(llm.Options{Mode: llm.ModeReplay, Cassettes: llm.NewCassetteStore(cassetteDir), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	misses, hashes := classifyPackets(t, client)
	if misses != pendingClassification {
		t.Errorf("%d dev-half pages have no cassette, want %d: a prompt, schema, config or packet byte moved, "+
			"or pendingClassification is out of date; record with go run ./cmd/dev record-pages", misses, pendingClassification)
	}
	files, err := os.ReadDir(filepath.Join(cassetteDir, jobs.JobPageClassification))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(files) != len(hashes)-misses {
		t.Errorf("%d classification cassettes, %d dev-half requests answered", len(files), len(hashes)-misses)
	}
	for _, f := range files {
		if h := strings.TrimSuffix(f.Name(), ".json"); !hashes[h] {
			t.Errorf("cassette %s answers no dev-half request: stale, or from the test half", f.Name())
		}
	}
}
