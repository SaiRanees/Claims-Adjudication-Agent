package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/audit"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// The tests in this file run the real handler, claims store and object store against
// Postgres and SeaweedFS: the local stack, whose variables dev test sets from .env, or CI's
// Database job. Without the variables they skip, unless TEST_S3_REQUIRED is set, as it is
// in CI's Database job, where a skip would let them look done without running.

// uploadAPI is the handler wired to the stack, with what the tests inspect behind it.
type uploadAPI struct {
	h       http.Handler
	pool    *pgxpool.Pool
	objects *documents.Objects
	auth    *platform.Authenticator
}

func newUploadAPI(t *testing.T, limiter *api.RateLimiter) *uploadAPI {
	t.Helper()
	dbURL := os.Getenv("TEST_DATABASE_URL")
	cfg := platform.S3Config{
		Endpoint:        os.Getenv("TEST_S3_ENDPOINT"),
		PublicEndpoint:  os.Getenv("TEST_S3_ENDPOINT"),
		Region:          "us-east-1",
		Bucket:          os.Getenv("TEST_S3_BUCKET"),
		AccessKeyID:     os.Getenv("TEST_S3_ACCESS_KEY"),
		SecretAccessKey: os.Getenv("TEST_S3_SECRET_KEY"),
	}
	if dbURL == "" || cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		const msg = "TEST_DATABASE_URL and the TEST_S3_* variables aren't all set; these tests need Postgres and SeaweedFS"
		if os.Getenv("TEST_S3_REQUIRED") != "" {
			t.Fatal(msg)
		}
		t.Skip(msg)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	objects := documents.NewObjects(cfg)
	if err := objects.Ping(ctx); err != nil {
		t.Fatalf("reaching SeaweedFS: %v", err)
	}
	auth, err := platform.NewAuthenticator(apiSecret, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := claims.NewStore(pool)
	h, err := api.NewHandler(api.NewServer(store, documents.NewService(store, objects), limiter, log), auth, log)
	if err != nil {
		t.Fatal(err)
	}
	return &uploadAPI{h: h, pool: pool, objects: objects, auth: auth}
}

func (a *uploadAPI) token(t *testing.T, role platform.Role, subject string) string {
	t.Helper()
	tok, err := a.auth.Issue(platform.Principal{Subject: subject, Role: role}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// newClaim creates a draft claim through the API.
func (a *uploadAPI) newClaim(t *testing.T, tok string) uuid.UUID {
	t.Helper()
	rec := create(t, a.h, tok, uuid.NewString(), validBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating a claim: %d %s", rec.Code, rec.Body)
	}
	var c apigen.Claim
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func (a *uploadAPI) register(tok string, claimID uuid.UUID, body string) *httptest.ResponseRecorder {
	return do(a.h, call{method: http.MethodPost, path: "/v1/claims/" + claimID.String() + "/documents",
		auth: "Bearer " + tok, body: body})
}

func (a *uploadAPI) complete(tok string, claimID, documentID uuid.UUID) *httptest.ResponseRecorder {
	return do(a.h, call{method: http.MethodPost, auth: "Bearer " + tok,
		path: "/v1/claims/" + claimID.String() + "/documents/" + documentID.String() + "/complete"})
}

// registerNew registers a file that must be new and returns how to upload it.
func (a *uploadAPI) registerNew(t *testing.T, tok string, claimID uuid.UUID, body string) apigen.DocumentUpload {
	t.Helper()
	rec := a.register(tok, claimID, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("registering: %d %s", rec.Code, rec.Body)
	}
	return decode[apigen.DocumentUpload](t, rec)
}

// completeAs completes a file and checks its status and rejection reason ("" for none).
func (a *uploadAPI) completeAs(t *testing.T, tok string, claimID, documentID uuid.UUID, status apigen.DocumentStatus, reason string) apigen.Document {
	t.Helper()
	rec := a.complete(tok, claimID, documentID)
	if rec.Code != http.StatusOK {
		t.Fatalf("completing: %d %s", rec.Code, rec.Body)
	}
	d := decode[apigen.Document](t, rec)
	got := ""
	if d.RejectionReason != nil {
		got = string(*d.RejectionReason)
	}
	if d.Status != status || got != reason {
		t.Fatalf("completed document is %s (%q), want %s (%q)", d.Status, got, status, reason)
	}
	return d
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decoding %s: %v", rec.Body, err)
	}
	return v
}

// synthetic is a small synthetic file. Two labels of one length give two files of one size.
func synthetic(label string) []byte {
	return []byte("%PDF-1.4\n% SYNTHETIC TEST DOCUMENT: " + label + "\n")
}

// uploadRequest is the body that registers file, declared with size and contentType.
func uploadRequest(t *testing.T, name, contentType string, size int64, file []byte) string {
	t.Helper()
	sum := sha256.Sum256(file)
	body, err := json.Marshal(map[string]any{
		"fileName": name, "contentType": contentType, "sizeBytes": size, "sha256": hex.EncodeToString(sum[:]),
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func pdf(t *testing.T, name string, file []byte) string {
	t.Helper()
	return uploadRequest(t, name, "application/pdf", int64(len(file)), file)
}

// putFile uploads body as a client would: a PUT to the signed URL with its headers.
func putFile(t *testing.T, up apigen.UploadInstructions, body []byte) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), string(up.Method), up.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range up.Headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// signals returns the claim's workflow signals, as "name payload" lines.
func signals(t *testing.T, pool *pgxpool.Pool, claimID uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT name, payload FROM outbox WHERE claim_id = $1 AND kind = 'workflow_signal' ORDER BY id`, claimID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var (
			name    string
			payload []byte
		)
		if err := rows.Scan(&name, &payload); err != nil {
			t.Fatal(err)
		}
		var fields map[string]string
		if err := json.Unmarshal(payload, &fields); err != nil {
			t.Fatal(err)
		}
		out = append(out, name+" "+fmt.Sprint(fields))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// chain reads the claim's audit chain, checks that every hash recomputes from the stored
// rows, and returns each event as "action documentId reason".
func chain(t *testing.T, pool *pgxpool.Pool, claimID uuid.UUID) []string {
	t.Helper()
	records, err := audit.List(context.Background(), pool, claimID)
	if err != nil {
		t.Fatal(err)
	}
	if err := audit.Verify(records); err != nil {
		t.Fatalf("the audit chain doesn't verify: %v", err)
	}
	var out []string
	for _, r := range records {
		for k, v := range r.Details {
			if s, ok := v.(string); k == "fileName" || ok && strings.Contains(s, ".pdf") {
				t.Errorf("event %d (%s) holds a file name: %s = %v", r.Seq, r.Action, k, v)
			}
		}
		line := r.Action
		if id, ok := r.Details["documentId"]; ok {
			line += " " + fmt.Sprint(id)
		}
		if reason, ok := r.Details["reason"]; ok {
			line += " " + fmt.Sprint(reason)
		}
		out = append(out, line)
	}
	return out
}

func setStatus(t *testing.T, pool *pgxpool.Pool, claimID uuid.UUID, status claims.Status) {
	t.Helper()
	// Moves the claim straight to a status, as claims_app, without the files submit needs.
	if _, err := pool.Exec(context.Background(),
		`UPDATE claims SET status = $2, submitted_at = coalesce(submitted_at, now()) WHERE id = $1`,
		claimID, string(status)); err != nil {
		t.Fatalf("moving the claim to %s: %v", status, err)
	}
}

func equalLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s =\n  %s\nwant\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func TestUploadsAgainstTheStack(t *testing.T) {
	a := newUploadAPI(t, api.NewRateLimiter(1000, time.Millisecond, time.Now))
	ctx := context.Background()
	alice := a.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	claimID := a.newClaim(t, alice)

	// 201: a new file, waiting for its upload, with a URL signed for its type and length.
	bill := synthetic("bill")
	first := a.registerNew(t, alice, claimID, pdf(t, "bill.pdf", bill))
	billID := first.Document.ID
	billSum := sha256.Sum256(bill)
	if d := first.Document; d.Status != apigen.DocumentStatusPendingUpload || d.ClaimID != claimID || d.FileName != "bill.pdf" ||
		d.SizeBytes != int64(len(bill)) || d.Sha256 != hex.EncodeToString(billSum[:]) || d.UploadedAt != nil {
		t.Errorf("registered document = %+v", d)
	}
	if up := first.Upload; up.Method != apigen.PUT || len(up.Headers) != 1 || up.Headers["Content-Type"] != "application/pdf" {
		t.Errorf("upload = %+v, want a PUT with only Content-Type to send", up)
	}
	if left := time.Until(first.Upload.ExpiresAt); left < 14*time.Minute || left > 15*time.Minute {
		t.Errorf("the upload URL expires in %v, want 15 minutes", left)
	}
	var key string
	if err := a.pool.QueryRow(ctx, `SELECT object_key FROM documents WHERE id = $1`, billID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if want := "quarantine/" + claimID.String() + "/" + billID.String(); key != want {
		t.Errorf("object key = %q, want %q: IDs only, never the file name", key, want)
	}

	// 200: the same file again, while it waits for its upload, is the same document.
	rec := a.register(alice, claimID, pdf(t, "bill.pdf", bill))
	if rec.Code != http.StatusOK {
		t.Fatalf("registering again: %d %s", rec.Code, rec.Body)
	}
	if again := decode[apigen.DocumentUpload](t, rec); again.Document.ID != billID || again.Upload.URL == "" {
		t.Errorf("registering again gave %+v, want document %s with a URL", again, billID)
	}

	// 409: the same SHA-256 declared with another size or type.
	problemOf(t, a.register(alice, claimID, uploadRequest(t, "bill.pdf", "application/pdf", int64(len(bill)+1), bill)),
		http.StatusConflict, "document_declared_differently")
	problemOf(t, a.register(alice, claimID, uploadRequest(t, "bill.png", "image/png", int64(len(bill)), bill)),
		http.StatusConflict, "document_declared_differently")

	// SeaweedFS refuses a body one byte longer than signed, then takes the file itself.
	if status := putFile(t, first.Upload, append(append([]byte{}, bill...), 'x')); status != http.StatusForbidden {
		t.Errorf("a PUT one byte longer than signed got %d, want 403", status)
	}
	if status := putFile(t, first.Upload, bill); status != http.StatusOK {
		t.Fatalf("uploading the bill got %d", status)
	}

	// Complete: uploaded, and the same result a second time.
	uploaded := a.completeAs(t, alice, claimID, billID, apigen.DocumentStatusUploaded, "")
	if uploaded.UploadedAt == nil {
		t.Error("an uploaded document has no uploadedAt")
	}
	if rec := a.complete(alice, claimID, billID); rec.Code != http.StatusOK || !jsonEqual(t, rec.Body.Bytes(), uploaded) {
		t.Errorf("completing again gave %d %s, want the first result", rec.Code, rec.Body)
	}

	// 409: a file already uploaded.
	problemOf(t, a.register(alice, claimID, pdf(t, "bill.pdf", bill)), http.StatusConflict, "document_already_uploaded")

	// Rejected: missing_object, for a file never uploaded.
	summary := a.registerNew(t, alice, claimID, pdf(t, "summary.pdf", synthetic("discharge summary")))
	a.completeAs(t, alice, claimID, summary.Document.ID, apigen.DocumentStatusRejected, "missing_object")

	// Rejected: checksum_mismatch, for other bytes of the declared length, which the signed
	// URL can't tell apart.
	receipt := synthetic("receipt A")
	receiptUp := a.registerNew(t, alice, claimID, pdf(t, "receipt.pdf", receipt))
	if status := putFile(t, receiptUp.Upload, synthetic("receipt B")); status != http.StatusOK {
		t.Fatalf("uploading other bytes of the same length got %d", status)
	}
	a.completeAs(t, alice, claimID, receiptUp.Document.ID, apigen.DocumentStatusRejected, "checksum_mismatch")

	// Rejected: size_mismatch, for an object of another size written straight to the key.
	pharmacy := a.registerNew(t, alice, claimID, pdf(t, "pharmacy.pdf", synthetic("pharmacy bill")))
	var pharmacyKey string
	if err := a.pool.QueryRow(ctx, `SELECT object_key FROM documents WHERE id = $1`, pharmacy.Document.ID).Scan(&pharmacyKey); err != nil {
		t.Fatal(err)
	}
	short := synthetic("short")
	if err := a.objects.Put(ctx, pharmacyKey, "application/pdf", bytes.NewReader(short), int64(len(short))); err != nil {
		t.Fatal(err)
	}
	a.completeAs(t, alice, claimID, pharmacy.Document.ID, apigen.DocumentStatusRejected, "size_mismatch")

	// A rejected file can be registered again, as a new document.
	retry := a.registerNew(t, alice, claimID, pdf(t, "receipt.pdf", receipt))
	if retry.Document.ID == receiptUp.Document.ID {
		t.Error("registering a rejected file again returned the rejected document")
	}

	// 404 for another submitter's claim, an unknown claim and an unknown document; 403 for staff.
	bob := a.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	other := pdf(t, "other.pdf", synthetic("other"))
	problemOf(t, a.register(bob, claimID, other), http.StatusNotFound, "not_found")
	problemOf(t, a.complete(bob, claimID, billID), http.StatusNotFound, "not_found")
	problemOf(t, a.register(alice, uuid.Must(uuid.NewV7()), other), http.StatusNotFound, "not_found")
	problemOf(t, a.complete(alice, claimID, uuid.Must(uuid.NewV7())), http.StatusNotFound, "not_found")
	problemOf(t, a.register(a.token(t, platform.RoleReviewer, "test-reviewer"), claimID, other), http.StatusForbidden, "role_not_allowed")

	// The workflow hears of the one uploaded file, by its ID only.
	equalLines(t, "workflow signals", signals(t, a.pool, claimID),
		[]string{"documentUploaded map[documentId:" + billID.String() + "]"})

	// A claim waiting for documents takes files too.
	setStatus(t, a.pool, claimID, claims.StatusAwaitingDocuments)
	late := a.registerNew(t, alice, claimID, pdf(t, "late.pdf", synthetic("late")))

	// 409 once the claim is submitted, for registering and for completing.
	setStatus(t, a.pool, claimID, claims.StatusSubmitted)
	problemOf(t, a.register(alice, claimID, other), http.StatusConflict, "claim_not_accepting_documents")
	problemOf(t, a.complete(alice, claimID, late.Document.ID), http.StatusConflict, "claim_not_accepting_documents")
	problemOf(t, a.complete(alice, claimID, billID), http.StatusConflict, "claim_not_accepting_documents")

	// Every step is in the claim's audit chain, which recomputes from the stored rows.
	equalLines(t, "audit chain", chain(t, a.pool, claimID), []string{
		"claim.created",
		"document.registered " + billID.String(),
		"document.uploaded " + billID.String(),
		"document.registered " + summary.Document.ID.String(),
		"document.rejected " + summary.Document.ID.String() + " missing_object",
		"document.registered " + receiptUp.Document.ID.String(),
		"document.rejected " + receiptUp.Document.ID.String() + " checksum_mismatch",
		"document.registered " + pharmacy.Document.ID.String(),
		"document.rejected " + pharmacy.Document.ID.String() + " size_mismatch",
		"document.registered " + retry.Document.ID.String(),
		"document.registered " + late.Document.ID.String(),
	})
}

// jsonEqual reports whether body decodes to want.
func jsonEqual(t *testing.T, body []byte, want apigen.Document) bool {
	t.Helper()
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var g, w any
	if json.Unmarshal(body, &g) != nil || json.Unmarshal(encoded, &w) != nil {
		return false
	}
	return reflect.DeepEqual(g, w)
}

func TestConcurrentCompletesRecordOneUpload(t *testing.T) {
	a := newUploadAPI(t, api.NewRateLimiter(1000, time.Millisecond, time.Now))
	alice := a.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	claimID := a.newClaim(t, alice)
	file := synthetic("concurrent")
	up := a.registerNew(t, alice, claimID, pdf(t, "bill.pdf", file))
	if status := putFile(t, up.Upload, file); status != http.StatusOK {
		t.Fatalf("uploading got %d", status)
	}

	const callers = 8
	bodies := make([]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := a.complete(alice, claimID, up.Document.ID)
			bodies[i] = fmt.Sprint(rec.Code, " ", rec.Body.String())
		}()
	}
	wg.Wait()
	for i, b := range bodies {
		if b != bodies[0] || !strings.HasPrefix(b, "200 ") || !strings.Contains(b, `"status":"uploaded"`) {
			t.Fatalf("complete %d gave %s, complete 0 gave %s; want one uploaded result for all", i, b, bodies[0])
		}
	}
	equalLines(t, "workflow signals", signals(t, a.pool, claimID),
		[]string{"documentUploaded map[documentId:" + up.Document.ID.String() + "]"})
	equalLines(t, "audit chain", chain(t, a.pool, claimID), []string{
		"claim.created",
		"document.registered " + up.Document.ID.String(),
		"document.uploaded " + up.Document.ID.String(),
	})
}

func TestAClaimHoldsAtMostFiftyFiles(t *testing.T) {
	a := newUploadAPI(t, api.NewRateLimiter(1000, time.Millisecond, time.Now))
	alice := a.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	claimID := a.newClaim(t, alice)
	file := func(i int) string {
		return pdf(t, fmt.Sprintf("file-%02d.pdf", i), synthetic(fmt.Sprintf("file %02d", i)))
	}

	ids := make([]uuid.UUID, claims.MaxFilesPerClaim)
	for i := range claims.MaxFilesPerClaim {
		ids[i] = a.registerNew(t, alice, claimID, file(i)).Document.ID
	}
	// A rejected file still counts toward the cap.
	a.completeAs(t, alice, claimID, ids[0], apigen.DocumentStatusRejected, "missing_object")

	problemOf(t, a.register(alice, claimID, file(claims.MaxFilesPerClaim)), http.StatusConflict, "too_many_documents")
	// The rejected file again would be a new file, so it's over the cap too.
	problemOf(t, a.register(alice, claimID, file(0)), http.StatusConflict, "too_many_documents")
	// A file still waiting for its upload isn't a new one: it comes back with a fresh URL.
	if rec := a.register(alice, claimID, file(1)); rec.Code != http.StatusOK || decode[apigen.DocumentUpload](t, rec).Document.ID != ids[1] {
		t.Errorf("registering a pending file at the cap: %d %s, want 200 with document %s", rec.Code, rec.Body, ids[1])
	}
	var n int
	if err := a.pool.QueryRow(context.Background(), `SELECT count(*) FROM documents WHERE claim_id = $1`, claimID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != claims.MaxFilesPerClaim {
		t.Errorf("the claim holds %d files, want %d", n, claims.MaxFilesPerClaim)
	}
	if got := len(chain(t, a.pool, claimID)); got != 1+claims.MaxFilesPerClaim+1 {
		t.Errorf("the audit chain has %d events, want %d", got, 1+claims.MaxFilesPerClaim+1)
	}
}

func TestRegisteringIsRateLimitedAgainstTheStack(t *testing.T) {
	a := newUploadAPI(t, api.NewRateLimiter(1, time.Hour, time.Now))
	alice := a.token(t, platform.RoleSubmitter, "test-"+uuid.NewString())
	claimID := a.newClaim(t, alice)
	a.registerNew(t, alice, claimID, pdf(t, "first.pdf", synthetic("first")))
	rec := a.register(alice, claimID, pdf(t, "second.pdf", synthetic("second")))
	problemOf(t, rec, http.StatusTooManyRequests, "rate_limited")
	if got := rec.Header().Get("Retry-After"); got != "3600" {
		t.Errorf("Retry-After = %q, want 3600", got)
	}
}
