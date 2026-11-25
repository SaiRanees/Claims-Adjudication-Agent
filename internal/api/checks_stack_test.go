package api_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	pdfcpuapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents/fixtures"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/relay"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

// The session 2 acceptance test (docs/plan/week-3.md): files go through the API into
// SeaweedFS, the outbox carries the signals to Temporal, a worker running the real
// workflow and CheckDocument with PDFium decides each file, and the claim is submitted.
//
// The test delivers its own claim's outbox rows rather than running the relay over the
// whole table, which holds rows other tests left behind, and its worker polls a task
// queue of its own. Holding delivery back also lets it replace a file after complete but
// before its check.

// stack is the API plus a worker and a delivery loop for one claim at a time.
type stack struct {
	*uploadAPI
	temporal  client.Client
	deliverer *relay.Temporal
}

func newStack(t *testing.T) *stack {
	t.Helper()
	a := newUploadAPI(t, api.NewRateLimiter(1000, time.Millisecond, time.Now))
	addr := os.Getenv("TEST_TEMPORAL_ADDRESS")
	if addr == "" {
		const msg = "TEST_TEMPORAL_ADDRESS is not set; this test needs Temporal with the claims namespace"
		if os.Getenv("TEST_S3_REQUIRED") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	c, err := platform.DialTemporal(ctx, platform.TemporalConfig{Address: addr, Namespace: "claims"}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)

	pdfium, err := documents.NewPDFium(documents.DefaultPDFiumConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pdfium.Close() })
	queue := "test-checks-" + uuid.NewString()
	w := worker.New(c, queue, worker.Options{})
	workflow.Register(w, &workflow.Activities{
		Checker: documents.NewChecker(claims.NewStore(a.pool), a.objects, pdfium),
	})
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.Stop)
	return &stack{uploadAPI: a, temporal: c, deliverer: relay.NewTemporal(c, queue)}
}

// deliver sends the claim's unpublished outbox rows to Temporal, in order, and marks them
// published, as the relay does.
func (s *stack) deliver(t *testing.T, claimID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	rows, err := s.pool.Query(ctx, `
		SELECT id, event_id, claim_id, kind, workflow_id, name, payload FROM outbox
		WHERE claim_id = $1 AND published_at IS NULL ORDER BY id`, claimID)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []relay.Message
	for rows.Next() {
		var m relay.Message
		if err := rows.Scan(&m.ID, &m.EventID, &m.ClaimID, &m.Kind, &m.WorkflowID, &m.Name, &m.Payload); err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if err := s.deliverer.Deliver(ctx, m); err != nil {
			t.Fatalf("delivering %s %s: %v", m.Kind, m.Name, err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE outbox SET published_at = now() WHERE id = $1`, m.ID); err != nil {
			t.Fatal(err)
		}
	}
}

// upload registers, uploads and completes one file, which must come back uploaded.
func (s *stack) upload(t *testing.T, tok string, claimID uuid.UUID, name, contentType string, file []byte) apigen.DocumentUpload {
	t.Helper()
	up := s.registerNew(t, tok, claimID, uploadRequest(t, name, contentType, int64(len(file)), file))
	if code := putFile(t, up.Upload, file); code != http.StatusOK {
		t.Fatalf("uploading %s: %d", name, code)
	}
	s.completeAs(t, tok, claimID, up.Document.ID, apigen.DocumentStatusUploaded, "")
	return up
}

func (s *stack) claim(t *testing.T, tok string, claimID uuid.UUID) apigen.Claim {
	t.Helper()
	rec := do(s.h, call{method: http.MethodGet, path: "/v1/claims/" + claimID.String(), auth: "Bearer " + tok})
	if rec.Code != http.StatusOK {
		t.Fatalf("reading the claim: %d %s", rec.Code, rec.Body)
	}
	return decode[apigen.Claim](t, rec)
}

func submitClaim(h http.Handler, tok string, claimID uuid.UUID) *httptest.ResponseRecorder {
	return do(h, call{method: http.MethodPost, path: "/v1/claims/" + claimID.String() + "/submit", auth: "Bearer " + tok})
}

func TestFileChecksAndSubmissionAgainstTheStack(t *testing.T) {
	s := newStack(t)
	alice := s.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	claimID := s.newClaim(t, alice)
	s.deliver(t, claimID) // starts claim-<id>

	pdfFile := fixtures.TextPDF(10)
	replacedFile := fixtures.TextPDF(2)
	files := []struct {
		name, contentType string
		data              []byte
		status            apigen.DocumentStatus
		reason            string
	}{
		{"bill.pdf", "application/pdf", pdfFile, apigen.DocumentStatusAccepted, ""},
		{"photo.jpg", "image/jpeg", fixtures.JPEG(t, 1200, 900, 6), apigen.DocumentStatusAccepted, ""},
		{"locked.pdf", "application/pdf", fixtures.EncryptedPDF(t, "synthetic-user"), apigen.DocumentStatusRejected, "encrypted"},
		{"empty-password.pdf", "application/pdf", fixtures.EncryptedPDF(t, ""), apigen.DocumentStatusRejected, "encrypted"},
		{"truncated.pdf", "application/pdf", pdfFile[:len(pdfFile)/2], apigen.DocumentStatusRejected, "unreadable"},
		{"huge.png", "image/png", fixtures.HugePNG(10_000, 5_001), apigen.DocumentStatusRejected, "image_too_large"},
		{"replaced.pdf", "application/pdf", replacedFile, apigen.DocumentStatusRejected, "checksum_mismatch"},
	}
	ids := make([]uuid.UUID, len(files))
	var replaced apigen.DocumentUpload
	for i, f := range files {
		up := s.upload(t, alice, claimID, f.name, f.contentType, f.data)
		ids[i] = up.Document.ID
		if f.name == "replaced.pdf" {
			replaced = up
		}
	}

	// The upload URL is still valid after complete: a body of the same length, one byte
	// different, replaces the object before the worker reads it.
	swapped := bytes.Clone(replacedFile)
	swapped[len(swapped)-10] ^= 0x01
	if code := putFile(t, replaced.Upload, swapped); code != http.StatusOK {
		t.Fatalf("replacing the file: %d", code)
	}

	// With checks pending, submit is a conflict.
	problemOf(t, submitClaim(s.h, alice, claimID), http.StatusConflict, "document_checks_pending")

	s.deliver(t, claimID) // the documentUploaded signals
	byID := waitForChecks(t, s, alice, claimID, len(files))
	for i, f := range files {
		d := byID[ids[i]]
		reason := ""
		if d.RejectionReason != nil {
			reason = string(*d.RejectionReason)
		}
		if d.Status != f.status || reason != f.reason {
			t.Errorf("%s is %s (%q), want %s (%q)", f.name, d.Status, reason, f.status, f.reason)
		}
		if d.CheckedAt == nil {
			t.Errorf("%s has no checkedAt", f.name)
		}
	}
	if p := byID[ids[0]].PageCount; p == nil || *p != 10 {
		t.Errorf("bill.pdf page count %v, want 10", p)
	}
	assertUpright(t, s.objects, claimID, ids[1])

	rec := submitClaim(s.h, alice, claimID)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submitting: %d %s", rec.Code, rec.Body)
	}
	if c := decode[apigen.Claim](t, rec); c.Status != apigen.ClaimStatusSubmitted || c.SubmittedAt == nil {
		t.Fatalf("submitted claim is %s, submittedAt %v", c.Status, c.SubmittedAt)
	}
	// Submitting again returns the current state.
	if rec := submitClaim(s.h, alice, claimID); rec.Code != http.StatusAccepted {
		t.Fatalf("submitting again: %d %s", rec.Code, rec.Body)
	}
	s.deliver(t, claimID) // submitted
	waitForWorkflow(t, s.temporal, "claim-"+claimID.String())

	// Every step is in the chain, and every hash recomputes from the stored rows.
	events := chain(t, s.pool, claimID)
	counts := map[string]int{}
	for _, e := range events {
		action, _, _ := strings.Cut(e, " ")
		counts[action]++
	}
	want := map[string]int{
		"claim.created": 1, "document.registered": len(files), "document.uploaded": len(files),
		"document.accepted": 2, "document.rejected": len(files) - 2, "claim.submitted": 1,
	}
	for action, n := range want {
		if counts[action] != n {
			t.Errorf("%d %s events, want %d\n%v", counts[action], action, n, events)
		}
	}
	if last := events[len(events)-1]; last != "claim.submitted" {
		t.Errorf("the last event is %q, want claim.submitted", last)
	}
}

// waitForChecks polls the claim until none of its n files is still uploaded.
func waitForChecks(t *testing.T, s *stack, tok string, claimID uuid.UUID, n int) map[uuid.UUID]apigen.Document {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		c := s.claim(t, tok, claimID)
		byID := map[uuid.UUID]apigen.Document{}
		pending := 0
		for _, d := range c.Documents {
			byID[d.ID] = d
			if d.Status == apigen.DocumentStatusUploaded {
				pending++
			}
		}
		if len(byID) == n && pending == 0 {
			return byID
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d files still unchecked after 2 minutes", pending, n)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func waitForWorkflow(t *testing.T, c client.Client, id string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := c.GetWorkflow(ctx, id, "").Get(ctx, nil); err != nil {
		t.Fatalf("workflow %s: %v", id, err)
	}
	desc, err := c.DescribeWorkflow(ctx, id, "")
	if err != nil {
		t.Fatal(err)
	}
	if desc.Status != enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		t.Fatalf("workflow %s is %v, want completed", id, desc.Status)
	}
}

// assertUpright reads the photo's PDF from the accepted copy and checks that its one page
// is turned 90° clockwise, which displays an orientation-6 photo upright.
func assertUpright(t *testing.T, objects *documents.Objects, claimID, documentID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	body, err := objects.Open(ctx, documents.PDFKey(claimID, documentID))
	if err != nil {
		t.Fatalf("reading the photo's PDF: %v", err)
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := pdfcpuapi.ReadContext(ctx, bytes.NewReader(data), model.NewStatelessConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if err := pc.EnsurePageCount(); err != nil {
		t.Fatal(err)
	}
	if pc.PageCount != 1 {
		t.Fatalf("the photo's PDF has %d pages", pc.PageCount)
	}
	_, _, inherited, err := pc.PageDict(ctx, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if inherited == nil || inherited.Rotate != 90 {
		t.Errorf("the photo's page /Rotate is %v, want 90", inherited)
	}
}

// Complete and submit, run against each other a hundred times, never both commit: the
// claim's row lock makes one wait for the other, and the second sees the first's result.
func TestCompleteAndSubmitNeverBothCommit(t *testing.T) {
	a := newUploadAPI(t, api.NewRateLimiter(1000, time.Millisecond, time.Now))
	alice := a.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	outcomes := map[string]int{}
	for run := range 100 {
		claimID := a.newClaim(t, alice)
		// One accepted file, set as the worker would, so the claim can be submitted.
		first := synthetic(fmt.Sprintf("race %03d accepted", run))
		up := a.registerNew(t, alice, claimID, pdf(t, "a.pdf", first))
		if code := putFile(t, up.Upload, first); code != http.StatusOK {
			t.Fatalf("uploading: %d", code)
		}
		a.completeAs(t, alice, claimID, up.Document.ID, apigen.DocumentStatusUploaded, "")
		accept(t, a.pool, up.Document.ID)
		// A second file, in the store and waiting for complete.
		second := synthetic(fmt.Sprintf("race %03d pending ", run))
		up2 := a.registerNew(t, alice, claimID, pdf(t, "b.pdf", second))
		if code := putFile(t, up2.Upload, second); code != http.StatusOK {
			t.Fatalf("uploading: %d", code)
		}

		var wg sync.WaitGroup
		var completed, submitted int
		release := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-release
			completed = a.complete(alice, claimID, up2.Document.ID).Code
		}()
		go func() {
			defer wg.Done()
			<-release
			// Complete reads the object store before it takes the claim's lock, so
			// submit, started at the same moment, would always lock first. Staggering
			// it by 0 to 19 ms lets complete win some runs too.
			time.Sleep(time.Duration(run%20) * time.Millisecond)
			submitted = submitClaim(a.h, alice, claimID).Code
		}()
		close(release)
		wg.Wait()

		switch {
		case completed == http.StatusOK && submitted == http.StatusConflict:
			outcomes["complete first"]++
		case completed == http.StatusConflict && submitted == http.StatusAccepted:
			outcomes["submit first"]++
		default:
			t.Fatalf("run %d: complete %d, submit %d", run, completed, submitted)
		}
		// The database agrees: a submitted claim has no uploaded file.
		var status string
		var uploaded int
		if err := a.pool.QueryRow(context.Background(), `
			SELECT c.status, (SELECT count(*) FROM documents WHERE claim_id = c.id AND status = 'uploaded')
			FROM claims c WHERE c.id = $1`, claimID).Scan(&status, &uploaded); err != nil {
			t.Fatal(err)
		}
		if status == "submitted" && uploaded != 0 {
			t.Fatalf("run %d: the claim is submitted with %d file(s) uploaded", run, uploaded)
		}
	}
	t.Logf("outcomes over 100 runs: %v", outcomes)
	if outcomes["complete first"] == 0 || outcomes["submit first"] == 0 {
		t.Errorf("only one order happened (%v), so the race wasn't exercised", outcomes)
	}
}

// accept marks an uploaded file accepted, as the worker's check would.
func accept(t *testing.T, pool *pgxpool.Pool, documentID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE documents SET status = 'accepted', checked_at = now(), page_count = 1, pdf_sha256 = sha256('x')
		WHERE id = $1`, documentID); err != nil {
		t.Fatal(err)
	}
}
