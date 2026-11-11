package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

var (
	apiSecret = []byte("api-test-secret-at-least-32-bytes!!!")
	apiNow    = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	member    = uuid.MustParse("01930000-0000-7000-8000-000000000201")
	// A version 7 UUID, as the services generate.
	newClaimID = uuid.MustParse("01931111-2222-7333-8444-555566667777")
	createdAt  = time.Date(2026, 11, 11, 12, 0, 1, 123456000, time.UTC)
)

const validBody = `{"memberId":"01930000-0000-7000-8000-000000000201","admittedOn":"2026-10-01",` +
	`"dischargedOn":"2026-10-05","hospitalName":"Synthetic City Hospital","claimedAmountPaise":12500000}`

func authenticator(t *testing.T, now time.Time) *platform.Authenticator {
	t.Helper()
	a, err := platform.NewAuthenticator(apiSecret, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func token(t *testing.T, role platform.Role, subject string) string {
	t.Helper()
	tok, err := authenticator(t, apiNow).Issue(platform.Principal{Subject: subject, Role: role}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// fakeClaims stands in for the claims store. Create hands Render what the real store
// would: the new row, in draft at version 1.
type fakeClaims struct {
	mu        sync.Mutex
	creates   []claims.CreateRequest
	createErr error
	replay    *claims.CreateResult
	claim     claims.Claim
	getErr    error
	docs      []claims.Document
}

func (f *fakeClaims) Create(_ context.Context, req claims.CreateRequest) (claims.CreateResult, error) {
	f.mu.Lock()
	f.creates = append(f.creates, req)
	f.mu.Unlock()
	if f.createErr != nil {
		return claims.CreateResult{}, f.createErr
	}
	if f.replay != nil {
		return *f.replay, nil
	}
	c := claims.Claim{
		ID: newClaimID, MemberID: req.Draft.MemberID, Status: claims.StatusDraft,
		AdmittedOn: req.Draft.AdmittedOn, DischargedOn: req.Draft.DischargedOn,
		HospitalName: req.Draft.HospitalName, ClaimedAmountPaise: req.Draft.ClaimedAmountPaise,
		WorkflowID: "claim-" + newClaimID.String(), CreatedBy: req.Principal, Version: 1,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
	resp, err := req.Render(c)
	if err != nil {
		return claims.CreateResult{}, err
	}
	return claims.CreateResult{ClaimID: c.ID, Response: resp}, nil
}

func (f *fakeClaims) Get(_ context.Context, id uuid.UUID) (claims.Claim, error) {
	if f.getErr != nil {
		return claims.Claim{}, f.getErr
	}
	if id != f.claim.ID {
		return claims.Claim{}, claims.ErrNotFound
	}
	return f.claim, nil
}

func (f *fakeClaims) Documents(_ context.Context, id uuid.UUID) ([]claims.Document, error) {
	if id != f.claim.ID {
		return nil, errors.New("documents asked for the wrong claim")
	}
	return f.docs, nil
}

func (f *fakeClaims) createCalls() []claims.CreateRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]claims.CreateRequest(nil), f.creates...)
}

func newAPI(t *testing.T, store *fakeClaims) (http.Handler, *api.Server) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := api.NewServer(store, log)
	h, err := api.NewHandler(srv, authenticator(t, apiNow), log)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	return h, srv
}

type call struct {
	method, path, auth, body string
	headers                  map[string]string
}

func do(h http.Handler, c call) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), c.method, c.path, strings.NewReader(c.body))
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.auth != "" {
		req.Header.Set("Authorization", c.auth)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func create(t *testing.T, h http.Handler, tok, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(h, call{method: http.MethodPost, path: "/v1/claims", auth: "Bearer " + tok, body: body,
		headers: map[string]string{"Idempotency-Key": key}})
}

func problemOf(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) apigen.Problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p apigen.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("not a problem: %s", rec.Body)
	}
	if int(p.Status) != status || p.Code == nil || *p.Code != code || p.Type == "" || p.Title == "" {
		t.Fatalf("problem = %s, want status %d and code %q", rec.Body, status, code)
	}
	return p
}

func pointers(p apigen.Problem) []string {
	var out []string
	if p.Errors != nil {
		for _, e := range *p.Errors {
			out = append(out, e.Pointer)
		}
	}
	return out
}

func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("response is not JSON: %s", got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
}

func TestHealthNeedsNoTokenAndReportsOK(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{})
	rec := do(h, call{method: http.MethodGet, path: "/healthz"})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("GET /healthz = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	assertJSON(t, rec.Body.Bytes(), `{"status":"ok"}`)
}

func TestHealthReportsUnavailableOnceDraining(t *testing.T) {
	h, srv := newAPI(t, &fakeClaims{})
	srv.Drain()
	problemOf(t, do(h, call{method: http.MethodGet, path: "/healthz"}), http.StatusServiceUnavailable, "shutting_down")
}

func TestProtectedOperationsNeedAValidBearerToken(t *testing.T) {
	expired, err := authenticator(t, apiNow.Add(-2*time.Hour)).
		Issue(platform.Principal{Subject: "alice", Role: platform.RoleSubmitter}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, auth, code string
	}{
		{"no Authorization header", "", "missing_token"},
		{"another scheme", "Basic YWxpY2U6c2VjcmV0", "missing_token"},
		{"a malformed token", "Bearer not-a-token", "invalid_token"},
		{"an expired token", "Bearer " + expired, "invalid_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeClaims{}
			h, _ := newAPI(t, store)
			rec := do(h, call{method: http.MethodPost, path: "/v1/claims", auth: tc.auth, body: validBody,
				headers: map[string]string{"Idempotency-Key": "k"}})
			problemOf(t, rec, http.StatusUnauthorized, tc.code)
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Errorf("WWW-Authenticate = %q, want a Bearer challenge", got)
			}
			if len(store.createCalls()) != 0 {
				t.Error("the store was called without a valid token")
			}
		})
	}
}

func TestCreateClaimIsForSubmittersOnly(t *testing.T) {
	for _, role := range []platform.Role{platform.RoleReviewer, platform.RoleAdmin} {
		t.Run(string(role), func(t *testing.T) {
			store := &fakeClaims{}
			h, _ := newAPI(t, store)
			problemOf(t, create(t, h, token(t, role, "staff-1"), "k", validBody), http.StatusForbidden, "role_not_allowed")
			if len(store.createCalls()) != 0 {
				t.Error("the store was called for a role that can't create claims")
			}
		})
	}
}

func TestCreateClaimChecksTheRequestAgainstTheContract(t *testing.T) {
	tests := []struct {
		name, key, body, wantPointer string
	}{
		{"missing field", "k", `{"memberId":"01930000-0000-7000-8000-000000000201","admittedOn":"2026-10-01","dischargedOn":"2026-10-05","claimedAmountPaise":1}`, "/hospitalName"},
		{"empty hospital name", "k", strings.Replace(validBody, `"Synthetic City Hospital"`, `""`, 1), "/hospitalName"},
		{"zero amount", "k", strings.Replace(validBody, "12500000", "0", 1), "/claimedAmountPaise"},
		{"unknown field", "k", strings.Replace(validBody, `{`, `{"payable":1,`, 1), "/payable"},
		{"impossible date", "k", strings.Replace(validBody, "2026-10-01", "2026-13-45", 1), "/admittedOn"},
		{"member ID not a UUID", "k", strings.Replace(validBody, "01930000-0000-7000-8000-000000000201", "member-1", 1), "/memberId"},
		{"no idempotency key", "", validBody, "/header/Idempotency-Key"},
		{"idempotency key with a space", "two words", validBody, "/header/Idempotency-Key"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeClaims{}
			h, _ := newAPI(t, store)
			c := call{method: http.MethodPost, path: "/v1/claims", auth: "Bearer " + token(t, platform.RoleSubmitter, "alice"),
				body: tc.body, headers: map[string]string{}}
			if tc.key != "" {
				c.headers["Idempotency-Key"] = tc.key
			}
			p := problemOf(t, do(h, c), http.StatusBadRequest, "validation_failed")
			if got := pointers(p); !contains(got, tc.wantPointer) {
				t.Errorf("errors point at %q, want %q among them", got, tc.wantPointer)
			}
			if len(store.createCalls()) != 0 {
				t.Error("the store was called with an invalid request")
			}
		})
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestCreateClaimRejectsMalformedJSON(t *testing.T) {
	store := &fakeClaims{}
	h, _ := newAPI(t, store)
	problemOf(t, create(t, h, token(t, platform.RoleSubmitter, "alice"), "k", `{"memberId":`), http.StatusBadRequest, "validation_failed")
	if len(store.createCalls()) != 0 {
		t.Error("the store was called with malformed JSON")
	}
}

func TestCreateClaimRejectsDischargeBeforeAdmission(t *testing.T) {
	store := &fakeClaims{}
	h, _ := newAPI(t, store)
	body := strings.Replace(validBody, `"dischargedOn":"2026-10-05"`, `"dischargedOn":"2026-09-30"`, 1)
	p := problemOf(t, create(t, h, token(t, platform.RoleSubmitter, "alice"), "k", body), http.StatusBadRequest, "validation_failed")
	if got := pointers(p); !contains(got, "/dischargedOn") {
		t.Errorf("errors point at %q, want /dischargedOn", got)
	}
	if len(store.createCalls()) != 0 {
		t.Error("the store was called with discharge before admission")
	}
}

func TestCreateClaimCreatesADraftAndPointsToIt(t *testing.T) {
	store := &fakeClaims{}
	h, _ := newAPI(t, store)
	rec := create(t, h, token(t, platform.RoleSubmitter, "alice"), "key-1", validBody)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != "/v1/claims/01931111-2222-7333-8444-555566667777" {
		t.Errorf("Location = %q", got)
	}
	assertJSON(t, rec.Body.Bytes(), `{
		"id": "01931111-2222-7333-8444-555566667777",
		"memberId": "01930000-0000-7000-8000-000000000201",
		"status": "draft",
		"admittedOn": "2026-10-01",
		"dischargedOn": "2026-10-05",
		"hospitalName": "Synthetic City Hospital",
		"claimedAmountPaise": 12500000,
		"version": 1,
		"documents": [],
		"timeline": [{"status": "draft", "version": 1, "at": "2026-11-11T12:00:01.123456Z"}],
		"createdAt": "2026-11-11T12:00:01.123456Z",
		"updatedAt": "2026-11-11T12:00:01.123456Z"
	}`)

	calls := store.createCalls()
	if len(calls) != 1 {
		t.Fatalf("store called %d times, want 1", len(calls))
	}
	got := calls[0]
	want := claims.Draft{
		MemberID:           member,
		AdmittedOn:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		DischargedOn:       time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		HospitalName:       "Synthetic City Hospital",
		ClaimedAmountPaise: 12500000,
	}
	if got.Principal != "alice" || got.IdempotencyKey != "key-1" || !reflect.DeepEqual(got.Draft, want) {
		t.Errorf("store got principal %q, key %q, draft %+v", got.Principal, got.IdempotencyKey, got.Draft)
	}
}

func TestCreateClaimHashesTheRequestNotItsFormatting(t *testing.T) {
	store := &fakeClaims{}
	h, _ := newAPI(t, store)
	tok := token(t, platform.RoleSubmitter, "alice")
	reordered := `{ "hospitalName": "Synthetic City Hospital", "claimedAmountPaise": 12500000,
		"memberId": "01930000-0000-7000-8000-000000000201", "dischargedOn": "2026-10-05", "admittedOn": "2026-10-01" }`
	for i, body := range []string{validBody, reordered, strings.Replace(validBody, "12500000", "12500001", 1)} {
		if rec := create(t, h, tok, "k", body); rec.Code != http.StatusCreated {
			t.Fatalf("request %d: status %d: %s", i, rec.Code, rec.Body)
		}
	}
	calls := store.createCalls()
	if calls[0].RequestHash != calls[1].RequestHash {
		t.Error("the same request, formatted differently, hashed differently")
	}
	if calls[0].RequestHash == calls[2].RequestHash {
		t.Error("a different amount hashed the same")
	}
}

func TestCreateClaimTurnsStoreOutcomesIntoProblems(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{claims.ErrIdempotencyKeyInUse, http.StatusConflict, "idempotency_key_in_use"},
		{claims.ErrIdempotencyKeyReused, http.StatusUnprocessableEntity, "idempotency_key_reused"},
		{claims.ErrMemberNotFound, http.StatusUnprocessableEntity, "member_not_found"},
		{claims.ErrNoPolicyYear, http.StatusUnprocessableEntity, "no_policy_year"},
		{errors.New("connection refused to db-host-7"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			h, _ := newAPI(t, &fakeClaims{createErr: tc.err})
			rec := create(t, h, token(t, platform.RoleSubmitter, "alice"), "k", validBody)
			problemOf(t, rec, tc.status, tc.code)
			if strings.Contains(rec.Body.String(), "db-host-7") {
				t.Error("the problem leaks the internal error")
			}
		})
	}
}

func TestCreateClaimReplaysTheStoredResponse(t *testing.T) {
	// Postgres hands jsonb back reformatted, so the stored body isn't byte-identical.
	stored := `{"id": "01931111-2222-7333-8444-555566667777", "memberId": "01930000-0000-7000-8000-000000000201",
		"status": "draft", "admittedOn": "2026-10-01", "dischargedOn": "2026-10-05",
		"hospitalName": "Synthetic City Hospital", "claimedAmountPaise": 12500000, "version": 1,
		"documents": [], "timeline": [{"at": "2026-11-11T12:00:01.123456Z", "status": "draft", "version": 1}],
		"createdAt": "2026-11-11T12:00:01.123456Z", "updatedAt": "2026-11-11T12:00:01.123456Z"}`
	store := &fakeClaims{replay: &claims.CreateResult{
		ClaimID:  newClaimID,
		Response: claims.Response{Status: http.StatusCreated, Body: []byte(stored)},
		Replayed: true,
	}}
	h, _ := newAPI(t, store)
	rec := create(t, h, token(t, platform.RoleSubmitter, "alice"), "k", validBody)
	if rec.Code != http.StatusCreated || rec.Header().Get("Location") != "/v1/claims/01931111-2222-7333-8444-555566667777" {
		t.Fatalf("replay = %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
	assertJSON(t, rec.Body.Bytes(), stored)
}

func storedClaim(owner string) claims.Claim {
	submitted := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC)
	return claims.Claim{
		ID: newClaimID, MemberID: member, Status: claims.StatusSubmitted,
		AdmittedOn: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), DischargedOn: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		HospitalName: "Synthetic City Hospital", ClaimedAmountPaise: 12500000,
		WorkflowID: "claim-" + newClaimID.String(), CreatedBy: owner, Version: 2,
		CreatedAt: createdAt, UpdatedAt: submitted, SubmittedAt: &submitted,
	}
}

func TestGetClaimShowsTheClaimToItsSubmitterAndToStaff(t *testing.T) {
	pages, uploaded := int32(3), time.Date(2026, 11, 11, 13, 0, 0, 0, time.UTC)
	docID := uuid.MustParse("01931111-2222-7333-8444-000000000001")
	sum := []byte(strings.Repeat("\xab", 32))
	for _, tc := range []struct {
		name string
		tok  func(t *testing.T) string
	}{
		{"its submitter", func(t *testing.T) string { return token(t, platform.RoleSubmitter, "alice") }},
		{"a reviewer", func(t *testing.T) string { return token(t, platform.RoleReviewer, "rev-1") }},
		{"an admin", func(t *testing.T) string { return token(t, platform.RoleAdmin, "admin-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeClaims{claim: storedClaim("alice"), docs: []claims.Document{{
				ID: docID, ClaimID: newClaimID, Status: "uploaded", FileName: "bill.pdf",
				ContentType: "application/pdf", SizeBytes: 2048, SHA256: sum, PageCount: &pages,
				CreatedAt: createdAt, UploadedAt: &uploaded,
			}}}
			h, _ := newAPI(t, store)
			rec := do(h, call{method: http.MethodGet, path: "/v1/claims/" + newClaimID.String(), auth: "Bearer " + tc.tok(t)})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
			}
			assertJSON(t, rec.Body.Bytes(), `{
				"id": "01931111-2222-7333-8444-555566667777",
				"memberId": "01930000-0000-7000-8000-000000000201",
				"status": "submitted",
				"admittedOn": "2026-10-01",
				"dischargedOn": "2026-10-05",
				"hospitalName": "Synthetic City Hospital",
				"claimedAmountPaise": 12500000,
				"version": 2,
				"documents": [{
					"id": "01931111-2222-7333-8444-000000000001",
					"claimId": "01931111-2222-7333-8444-555566667777",
					"status": "uploaded",
					"fileName": "bill.pdf",
					"contentType": "application/pdf",
					"sizeBytes": 2048,
					"sha256": "`+strings.Repeat("ab", 32)+`",
					"pageCount": 3,
					"createdAt": "2026-11-11T12:00:01.123456Z",
					"uploadedAt": "2026-11-11T13:00:00Z"
				}],
				"timeline": [
					{"status": "draft", "version": 1, "at": "2026-11-11T12:00:01.123456Z"},
					{"status": "submitted", "version": 2, "at": "2026-11-12T08:00:00Z"}
				],
				"createdAt": "2026-11-11T12:00:01.123456Z",
				"updatedAt": "2026-11-12T08:00:00Z",
				"submittedAt": "2026-11-12T08:00:00Z"
			}`)
		})
	}
}

func TestGetClaimHidesOtherSubmittersClaims(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{claim: storedClaim("alice")})
	rec := do(h, call{method: http.MethodGet, path: "/v1/claims/" + newClaimID.String(),
		auth: "Bearer " + token(t, platform.RoleSubmitter, "bob")})
	problemOf(t, rec, http.StatusNotFound, "not_found")
}

func TestGetClaimReportsAnUnknownClaim(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{claim: storedClaim("alice")})
	rec := do(h, call{method: http.MethodGet, path: "/v1/claims/01931111-2222-7333-8444-999999999999",
		auth: "Bearer " + token(t, platform.RoleReviewer, "rev-1")})
	problemOf(t, rec, http.StatusNotFound, "not_found")
}

func TestGetClaimRejectsAnIDThatIsNotAUUID(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{})
	rec := do(h, call{method: http.MethodGet, path: "/v1/claims/claim-42", auth: "Bearer " + token(t, platform.RoleReviewer, "rev-1")})
	p := problemOf(t, rec, http.StatusBadRequest, "validation_failed")
	if got := pointers(p); !contains(got, "/path/claimId") {
		t.Errorf("errors point at %q, want /path/claimId", got)
	}
}

func TestOperationsNotBuiltYetAnswer501(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{})
	for _, c := range []call{
		{method: http.MethodGet, path: "/v1/claims/" + newClaimID.String() + "/adjudication", auth: "Bearer " + token(t, platform.RoleSubmitter, "alice")},
		{method: http.MethodGet, path: "/v1/review-tasks", auth: "Bearer " + token(t, platform.RoleReviewer, "rev-1")},
	} {
		problemOf(t, do(h, c), http.StatusNotImplemented, "not_implemented")
	}
}

func TestUnknownPathsAndMethodsGetProblems(t *testing.T) {
	h, _ := newAPI(t, &fakeClaims{})
	tok := "Bearer " + token(t, platform.RoleSubmitter, "alice")
	problemOf(t, do(h, call{method: http.MethodGet, path: "/v2/claims", auth: tok}), http.StatusNotFound, "not_found")
	// kin-openapi's router also accepts a trailing slash, which the dispatching ServeMux doesn't.
	problemOf(t, do(h, call{method: http.MethodGet, path: "/v1/review-tasks/", auth: tok}), http.StatusNotFound, "not_found")
	// ...and an empty path parameter: GET /v1/claims would otherwise reach getClaim with no ID.
	for _, method := range []string{http.MethodDelete, http.MethodGet} {
		rec := do(h, call{method: method, path: "/v1/claims", auth: tok})
		problemOf(t, rec, http.StatusMethodNotAllowed, "method_not_allowed")
		if got := rec.Header().Get("Allow"); got != "POST" {
			t.Errorf("%s /v1/claims: Allow = %q, want POST", method, got)
		}
	}
}

func TestCreateClaimRefusesAnOversizedBody(t *testing.T) {
	store := &fakeClaims{}
	h, _ := newAPI(t, store)
	body := strings.Replace(validBody, "Synthetic City Hospital", strings.Repeat("x", 2<<20), 1)
	problemOf(t, create(t, h, token(t, platform.RoleSubmitter, "alice"), "k", body), http.StatusRequestEntityTooLarge, "body_too_large")
	if len(store.createCalls()) != 0 {
		t.Error("the store was called with an oversized body")
	}
}
