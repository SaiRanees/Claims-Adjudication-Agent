package extraction_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/SaiRanees/Claims-Adjudication-Agent/evals/extraction"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/datagen"
	jobs "github.com/SaiRanees/Claims-Adjudication-Agent/internal/extraction"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/llm"
)

// smokePacket's files are pinned by SHA-256 on both CI jobs
// (evals/extraction/pins_test.go), so the smoke requests hash the same on
// Linux and Windows.
const smokePacket = "seeded-surgery-implant-03"

// smokeCassettes holds the recorded answers to the smoke requests, one per
// job. They change only through a record run (CLAUDE.md, rule 9).
var smokeCassettes = filepath.Join("testdata", "cassettes")

// smokeCase is one job's smoke request and what its answer must show.
type smokeCase struct {
	job     string
	req     llm.Request
	packet  datagen.Packet
	billNum string
}

// smokeCases renders the packet and builds one request per job: its photo
// ID page to classify, and its two-page hospital bill to extract.
func smokeCases(t *testing.T) []smokeCase {
	t.Helper()
	specs, err := extraction.Specs()
	if err != nil {
		t.Fatal(err)
	}
	var spec *datagen.Spec
	for i := range specs {
		if specs[i].ID == smokePacket {
			spec = &specs[i]
		}
	}
	if spec == nil {
		t.Fatalf("no spec %s", smokePacket)
	}
	p, err := datagen.Render(*spec)
	if err != nil {
		t.Fatal(err)
	}
	file := func(name string) []byte {
		for _, f := range p.Files {
			if f.Name == name {
				return f.Data
			}
		}
		t.Fatalf("%s has no %s", smokePacket, name)
		return nil
	}
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	var billNum string
	for _, b := range p.Truth.Bills {
		if b.File == "hospital-bill.pdf" {
			billNum = b.Number
		}
	}
	var cases []smokeCase
	for _, c := range []struct{ job, file string }{
		{jobs.JobPageClassification, "photo-id.pdf"},
		{jobs.JobLineItems, "hospital-bill.pdf"},
	} {
		req, err := jobs.NewRequest(cfg, c.job, []llm.Part{llm.FilePart(llm.MIMEPDF, file(c.file))}, 0)
		if err != nil {
			t.Fatal(err)
		}
		cases = append(cases, smokeCase{job: c.job, req: req, packet: p, billNum: billNum})
	}
	return cases
}

// checkAnswer decodes an answer strictly and checks it is the right kind of
// answer. Accuracy is session 8's eval; this proves the plumbing.
func checkAnswer(t *testing.T, c smokeCase, res llm.Result) {
	t.Helper()
	if err := res.Failed(); err != nil {
		t.Fatalf("%s: %v", c.job, err)
	}
	switch c.job {
	case jobs.JobPageClassification:
		var out jobs.PageClassification
		if err := llm.Decode(c.req.Schema, res.Output, &out); err != nil {
			t.Fatalf("%s: %v\n%s", c.job, err, res.Output)
		}
		if out.PageType != "photo_id" {
			t.Errorf("%s: the photo ID page came back as %s", c.job, out.PageType)
		}
	case jobs.JobLineItems:
		var out jobs.LineItems
		if err := llm.Decode(c.req.Schema, res.Output, &out); err != nil {
			t.Fatalf("%s: %v\n%s", c.job, err, res.Output)
		}
		if len(out.Lines) == 0 {
			t.Errorf("%s: no lines", c.job)
		}
		if out.Bill.BillNumber == nil || *out.Bill.BillNumber != c.billNum {
			t.Errorf("%s: bill number %v, printed %s", c.job, out.Bill.BillNumber, c.billNum)
		}
		for _, b := range c.packet.Truth.Bills {
			if b.File == "hospital-bill.pdf" {
				t.Logf("%s: %d lines extracted, %d printed", c.job, len(out.Lines), len(b.Lines))
			}
		}
	default:
		t.Fatalf("no check for job %s", c.job)
	}
}

// TestReplaySmoke replays each job's smoke cassette, with no key and no
// network, on CI's Linux and Windows jobs. A miss means the request's hash
// moved: a prompt, schema, config or packet byte changed, and the smoke
// cassettes need a record run (go run ./cmd/dev smoke).
func TestReplaySmoke(t *testing.T) {
	cfg, err := llm.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	client, err := llm.NewClient(llm.Options{Mode: llm.ModeReplay, Cassettes: llm.NewCassetteStore(smokeCassettes), Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range smokeCases(t) {
		t.Run(c.job, func(t *testing.T) {
			res, err := client.Call(t.Context(), c.req)
			if errors.Is(err, llm.ErrCassetteMiss) {
				t.Fatalf("%v; record it with go run ./cmd/dev smoke", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !res.Replayed {
				t.Error("replay didn't come from a cassette")
			}
			checkAnswer(t, c, res)
		})
	}
}
