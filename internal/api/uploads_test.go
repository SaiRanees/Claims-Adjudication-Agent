package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// fakeDocuments stands in for documents.Service and records what the handlers asked of it.
type fakeDocuments struct {
	mu          sync.Mutex
	registers   []claims.NewDocument
	completes   []uuid.UUID
	registered  documents.Registered
	registerErr error
	completed   claims.Document
	completeErr error
}

func (f *fakeDocuments) Register(_ context.Context, nd claims.NewDocument) (documents.Registered, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registers = append(f.registers, nd)
	return f.registered, f.registerErr
}

func (f *fakeDocuments) Complete(_ context.Context, _ string, _, documentID uuid.UUID) (claims.Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completes = append(f.completes, documentID)
	return f.completed, f.completeErr
}

func (f *fakeDocuments) calls() (registers, completes int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.registers), len(f.completes)
}

var (
	docID     = uuid.MustParse("01931111-2222-7333-8444-0000000000d1")
	docSHA256 = strings.Repeat("ab", 32)
)

const uploadBody = `{"fileName":"bill.pdf","contentType":"application/pdf","sizeBytes":2048,` +
	`"sha256":"abababababababababababababababababababababababababababababababab"}`

func pendingDocument() claims.Document {
	return claims.Document{
		ID: docID, ClaimID: newClaimID, Status: claims.DocumentPendingUpload, FileName: "bill.pdf",
		ContentType: "application/pdf", SizeBytes: 2048, SHA256: []byte(strings.Repeat("\xab", 32)),
		ObjectKey: "quarantine/" + newClaimID.String() + "/" + docID.String(), CreatedAt: createdAt,
	}
}

func registration(created bool) documents.Registered {
	return documents.Registered{
		Document: pendingDocument(),
		Upload: documents.Upload{
			URL:       "http://localhost:8333/claim-documents/quarantine/x?X-Amz-Signature=abc",
			Headers:   map[string]string{"Content-Type": "application/pdf"},
			ExpiresAt: time.Date(2026, 11, 11, 12, 15, 1, 0, time.UTC),
		},
		Created: created,
	}
}

func registerCall(h http.Handler, tok, body string) *httptest.ResponseRecorder {
	return do(h, call{method: http.MethodPost, path: "/v1/claims/" + newClaimID.String() + "/documents",
		auth: "Bearer " + tok, body: body})
}

func completeCall(h http.Handler, tok string) *httptest.ResponseRecorder {
	return do(h, call{method: http.MethodPost,
		path: "/v1/claims/" + newClaimID.String() + "/documents/" + docID.String() + "/complete", auth: "Bearer " + tok})
}

func TestUploadOperationsAreForSubmittersOnly(t *testing.T) {
	for _, role := range []platform.Role{platform.RoleReviewer, platform.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			docs := &fakeDocuments{registered: registration(true), completed: pendingDocument()}
			h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(10, time.Second, time.Now))
			tok := token(t, role, "staff-1")
			problemOf(t, registerCall(h, tok, uploadBody), http.StatusForbidden, "role_not_allowed")
			problemOf(t, completeCall(h, tok), http.StatusForbidden, "role_not_allowed")
			if r, c := docs.calls(); r != 0 || c != 0 {
				t.Errorf("the service was called %d and %d times for a role that can't upload", r, c)
			}
		})
	}
}

const wantUpload = `{
	"document": {
		"id": "01931111-2222-7333-8444-0000000000d1",
		"claimId": "01931111-2222-7333-8444-555566667777",
		"status": "pending_upload",
		"fileName": "bill.pdf",
		"contentType": "application/pdf",
		"sizeBytes": 2048,
		"sha256": "abababababababababababababababababababababababababababababababab",
		"createdAt": "2026-11-11T12:00:01.123456Z"
	},
	"upload": {
		"method": "PUT",
		"url": "http://localhost:8333/claim-documents/quarantine/x?X-Amz-Signature=abc",
		"headers": {"Content-Type": "application/pdf"},
		"expiresAt": "2026-11-11T12:15:01Z"
	}
}`

func TestCreateDocumentUploadReturnsTheDocumentAndItsUploadURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		created bool
		status  int
	}{
		{"a new file", true, http.StatusCreated},
		{"a file still waiting for its upload", false, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := &fakeDocuments{registered: registration(tc.created)}
			h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(10, time.Second, time.Now))
			rec := registerCall(h, token(t, platform.RoleSubmitter, "alice"), uploadBody)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.status, rec.Body)
			}
			assertJSON(t, rec.Body.Bytes(), wantUpload)
			want := claims.NewDocument{
				Principal: "alice", ClaimID: newClaimID, FileName: "bill.pdf", ContentType: "application/pdf",
				SizeBytes: 2048,
			}
			copy(want.SHA256[:], strings.Repeat("\xab", 32))
			if len(docs.registers) != 1 || docs.registers[0] != want {
				t.Errorf("the service got %+v, want %+v", docs.registers, want)
			}
		})
	}
}

func TestCreateDocumentUploadChecksTheRequestAgainstTheContract(t *testing.T) {
	tests := []struct {
		name, body, wantPointer string
	}{
		{"upper-case hash", strings.Replace(uploadBody, docSHA256, strings.ToUpper(docSHA256), 1), "/sha256"},
		{"short hash", strings.Replace(uploadBody, docSHA256, docSHA256[1:], 1), "/sha256"},
		{"empty file", strings.Replace(uploadBody, `"sizeBytes":2048`, `"sizeBytes":0`, 1), "/sizeBytes"},
		{"over 25 MiB", strings.Replace(uploadBody, `"sizeBytes":2048`, `"sizeBytes":26214401`, 1), "/sizeBytes"},
		{"another content type", strings.Replace(uploadBody, "application/pdf", "text/plain", 1), "/contentType"},
		{"no file name", strings.Replace(uploadBody, `"fileName":"bill.pdf",`, "", 1), "/fileName"},
		{"an unknown field", strings.Replace(uploadBody, `{`, `{"objectKey":"x",`, 1), "/objectKey"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			docs := &fakeDocuments{registered: registration(true)}
			h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(10, time.Second, time.Now))
			p := problemOf(t, registerCall(h, token(t, platform.RoleSubmitter, "alice"), tc.body),
				http.StatusBadRequest, "validation_failed")
			if got := pointers(p); !contains(got, tc.wantPointer) {
				t.Errorf("errors point at %q, want %q among them", got, tc.wantPointer)
			}
			if r, _ := docs.calls(); r != 0 {
				t.Error("the service was called with an invalid request")
			}
		})
	}
}

func TestCreateDocumentUploadTurnsOutcomesIntoProblems(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{claims.ErrNotFound, http.StatusNotFound, "not_found"},
		{claims.ErrNotAcceptingDocuments, http.StatusConflict, "claim_not_accepting_documents"},
		{claims.ErrAlreadyUploaded, http.StatusConflict, "document_already_uploaded"},
		{claims.ErrDeclaredDifferently, http.StatusConflict, "document_declared_differently"},
		{claims.ErrTooManyFiles, http.StatusConflict, "too_many_documents"},
		{errors.New("connection refused to db-host-7"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			docs := &fakeDocuments{registerErr: tc.err}
			h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(10, time.Second, time.Now))
			rec := registerCall(h, token(t, platform.RoleSubmitter, "alice"), uploadBody)
			problemOf(t, rec, tc.status, tc.code)
			if strings.Contains(rec.Body.String(), "db-host-7") {
				t.Error("the problem leaks the internal error")
			}
		})
	}
}

func TestCompleteDocumentUploadReturnsTheDocument(t *testing.T) {
	rejected := pendingDocument()
	rejected.Status = claims.DocumentRejected
	reason := claims.RejectChecksumMismatch
	rejected.RejectionReason = &reason
	docs := &fakeDocuments{completed: rejected}
	h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(10, time.Second, time.Now))
	rec := completeCall(h, token(t, platform.RoleSubmitter, "alice"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	assertJSON(t, rec.Body.Bytes(), `{
		"id": "01931111-2222-7333-8444-0000000000d1",
		"claimId": "01931111-2222-7333-8444-555566667777",
		"status": "rejected",
		"rejectionReason": "checksum_mismatch",
		"fileName": "bill.pdf",
		"contentType": "application/pdf",
		"sizeBytes": 2048,
		"sha256": "abababababababababababababababababababababababababababababababab",
		"createdAt": "2026-11-11T12:00:01.123456Z"
	}`)
	if len(docs.completes) != 1 || docs.completes[0] != docID {
		t.Errorf("the service completed %v, want [%s]", docs.completes, docID)
	}
}

func TestCompleteDocumentUploadTurnsOutcomesIntoProblems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"no such claim", claims.ErrNotFound, http.StatusNotFound, "not_found"},
		{"no such document", claims.ErrDocumentNotFound, http.StatusNotFound, "not_found"},
		{"claim submitted", claims.ErrNotAcceptingDocuments, http.StatusConflict, "claim_not_accepting_documents"},
		{"store down", errors.New("connection refused to s3-host-7"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docs := &fakeDocuments{completeErr: tc.err}
			h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(10, time.Second, time.Now))
			rec := completeCall(h, token(t, platform.RoleSubmitter, "alice"))
			problemOf(t, rec, tc.status, tc.code)
			if strings.Contains(rec.Body.String(), "s3-host-7") {
				t.Error("the problem leaks the internal error")
			}
		})
	}
}

func TestRegisteringIsRateLimitedPerCaller(t *testing.T) {
	now := apiNow
	clock := func() time.Time { return now }
	docs := &fakeDocuments{registered: registration(true)}
	h, _ := newAPIWith(t, &fakeClaims{}, docs, api.NewRateLimiter(2, 10*time.Second, clock))
	alice, bob := token(t, platform.RoleSubmitter, "alice"), token(t, platform.RoleSubmitter, "bob")

	steps := []struct {
		name       string
		advance    time.Duration
		tok        string
		status     int
		retryAfter string
	}{
		{"alice's first", 0, alice, http.StatusCreated, ""},
		{"alice's second, the rest of her burst", 0, alice, http.StatusCreated, ""},
		{"alice's third, over her burst", 0, alice, http.StatusTooManyRequests, "10"},
		{"bob's first, from his own bucket", 0, bob, http.StatusCreated, ""},
		{"alice again 3.5s later, rounded up", 3500 * time.Millisecond, alice, http.StatusTooManyRequests, "7"},
		{"alice once a token is back", 6500 * time.Millisecond, alice, http.StatusCreated, ""},
		{"alice straight after", 0, alice, http.StatusTooManyRequests, "10"},
	}
	for _, s := range steps {
		now = now.Add(s.advance)
		rec := registerCall(h, s.tok, uploadBody)
		if s.status == http.StatusTooManyRequests {
			problemOf(t, rec, http.StatusTooManyRequests, "rate_limited")
		} else if rec.Code != s.status {
			t.Fatalf("%s: status = %d, want %d; body %s", s.name, rec.Code, s.status, rec.Body)
		}
		if got := rec.Header().Get("Retry-After"); got != s.retryAfter {
			t.Errorf("%s: Retry-After = %q, want %q", s.name, got, s.retryAfter)
		}
	}
	if r, _ := docs.calls(); r != 4 {
		t.Errorf("the service was called %d times, want 4: never for a limited request", r)
	}
}
