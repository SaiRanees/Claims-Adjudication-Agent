package claims_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

// The seed's synthetic member, covered by the 2026-04-01 to 2027-03-31 policy year
// (db/seed/dev.sql).
var seededMember = uuid.MustParse("01930000-0000-7000-8000-000000000201")

// connect opens a pool on TEST_DATABASE_URL, the migrated and seeded database, as
// claims_app so the grants are exercised too. claims_app can't delete claims, so each test
// uses its own principal ("test-<uuid>") and the rows stay behind; `dev reset` clears them.
func connect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; these tests need the migrated, seeded database")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newPrincipal() string { return "test-" + uuid.NewString() }

func day(s string) time.Time {
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return d
}

func draft() claims.Draft {
	return claims.Draft{
		MemberID:           seededMember,
		AdmittedOn:         day("2026-10-01"),
		DischargedOn:       day("2026-10-05"),
		HospitalName:       "Synthetic Test Hospital",
		ClaimedAmountPaise: 12500000,
	}
}

// recorder is a Render that remembers each claim it was given and returns a body naming it.
type recorder struct {
	mu   sync.Mutex
	seen []claims.Claim
}

func (r *recorder) render(c claims.Claim) (claims.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, c)
	return claims.Response{
		Status: 201,
		Body:   fmt.Appendf(nil, `{"id":%q,"status":%q,"version":%d}`, c.ID, c.Status, c.Version),
	}, nil
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.seen)
}

func request(principal, key, body string, d claims.Draft, render func(claims.Claim) (claims.Response, error)) claims.CreateRequest {
	return claims.CreateRequest{
		Principal:      principal,
		IdempotencyKey: key,
		RequestHash:    sha256.Sum256([]byte(body)),
		Draft:          d,
		Render:         render,
	}
}

func countClaims(t *testing.T, pool *pgxpool.Pool, principal string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM claims WHERE created_by = $1`, principal).Scan(&n); err != nil {
		t.Fatalf("counting claims: %v", err)
	}
	return n
}

func assertJSONEqual(t *testing.T, what string, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: not JSON: %s", what, got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("%s: want is not JSON: %s", what, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s = %s, want %s", what, got, want)
	}
}

func TestCreateWritesTheClaimItsOutboxRowAndTheKeyTogether(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	store := claims.NewStore(pool)
	principal := newPrincipal()
	rec := &recorder{}

	res, err := store.Create(ctx, request(principal, "k1", "body", draft(), rec.render))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if res.Replayed {
		t.Error("a first request was reported as a replay")
	}
	if rec.calls() != 1 {
		t.Fatalf("Render called %d times, want 1", rec.calls())
	}
	c := rec.seen[0]
	if c.ID != res.ClaimID || c.Status != claims.StatusDraft || c.Version != 1 || c.CreatedBy != principal {
		t.Errorf("Render got %+v", c)
	}
	wantBody := fmt.Appendf(nil, `{"id":%q,"status":"draft","version":1}`, res.ClaimID)
	if res.Response.Status != 201 {
		t.Errorf("response status = %d, want 201", res.Response.Status)
	}
	assertJSONEqual(t, "response body", res.Response.Body, wantBody)

	var (
		status, workflowID, createdBy string
		admitted, discharged          time.Time
		hospital                      string
		amount                        int64
	)
	if err := pool.QueryRow(ctx, `
		SELECT status, workflow_id, created_by, admitted_on, discharged_on, hospital_name, claimed_amount_paise
		FROM claims WHERE id = $1`, res.ClaimID).
		Scan(&status, &workflowID, &createdBy, &admitted, &discharged, &hospital, &amount); err != nil {
		t.Fatalf("reading the claim: %v", err)
	}
	if status != "draft" || workflowID != "claim-"+res.ClaimID.String() || createdBy != principal ||
		!admitted.Equal(day("2026-10-01")) || !discharged.Equal(day("2026-10-05")) ||
		hospital != "Synthetic Test Hospital" || amount != 12500000 {
		t.Errorf("claim row = %s %s %s %v %v %s %d", status, workflowID, createdBy, admitted, discharged, hospital, amount)
	}

	var (
		kind, outboxWorkflow, name string
		payload                    []byte
		published                  *time.Time
	)
	if err := pool.QueryRow(ctx, `
		SELECT kind, workflow_id, name, payload, published_at FROM outbox WHERE claim_id = $1`, res.ClaimID).
		Scan(&kind, &outboxWorkflow, &name, &payload, &published); err != nil {
		t.Fatalf("reading the outbox row: %v", err)
	}
	if kind != "workflow_start" || outboxWorkflow != "claim-"+res.ClaimID.String() || name != "ClaimWorkflow" || published != nil {
		t.Errorf("outbox row = %s %s %s published=%v", kind, outboxWorkflow, name, published)
	}
	assertJSONEqual(t, "outbox payload", payload, fmt.Appendf(nil, `{"claimId":%q}`, res.ClaimID))

	var (
		hash       []byte
		respStatus int
		respBody   []byte
		claimID    uuid.UUID
		ttl        time.Duration
	)
	if err := pool.QueryRow(ctx, `
		SELECT request_hash, response_status, response_body, claim_id,
		       (extract(epoch FROM expires_at - created_at) * 1e9)::bigint
		FROM idempotency_keys WHERE principal = $1 AND idempotency_key = 'k1'`, principal).
		Scan(&hash, &respStatus, &respBody, &claimID, &ttl); err != nil {
		t.Fatalf("reading the idempotency key: %v", err)
	}
	wantHash := sha256.Sum256([]byte("body"))
	if string(hash) != string(wantHash[:]) || respStatus != 201 || claimID != res.ClaimID || ttl != 24*time.Hour {
		t.Errorf("key row: hash ok=%v status=%d claim=%s ttl=%v", string(hash) == string(wantHash[:]), respStatus, claimID, ttl)
	}
	assertJSONEqual(t, "stored response", respBody, wantBody)
}

func TestCreateWithTheSameKeyAndRequestMakesOneClaimAndReplaysTheFirstResponse(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	store := claims.NewStore(pool)
	principal := newPrincipal()
	rec := &recorder{}

	first, err := store.Create(ctx, request(principal, "same-key", "body", draft(), rec.render))
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := store.Create(ctx, request(principal, "same-key", "body", draft(), rec.render))
	if err != nil {
		t.Fatalf("second Create: %v", err)
	}
	if !second.Replayed || second.ClaimID != first.ClaimID || second.Response.Status != 201 {
		t.Fatalf("second = %+v, want a replay of claim %s", second, first.ClaimID)
	}
	assertJSONEqual(t, "replayed body", second.Response.Body, first.Response.Body)
	if rec.calls() != 1 {
		t.Errorf("Render called %d times, want 1", rec.calls())
	}
	if n := countClaims(t, pool, principal); n != 1 {
		t.Errorf("%d claims, want 1", n)
	}
	var outboxRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE claim_id = $1`, first.ClaimID).Scan(&outboxRows); err != nil {
		t.Fatal(err)
	}
	if outboxRows != 1 {
		t.Errorf("%d outbox rows, want 1", outboxRows)
	}
}

func TestCreateRefusesTheKeyForADifferentRequest(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	store := claims.NewStore(pool)
	principal := newPrincipal()
	rec := &recorder{}

	if _, err := store.Create(ctx, request(principal, "k", "body one", draft(), rec.render)); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	other := draft()
	other.ClaimedAmountPaise = 999
	_, err := store.Create(ctx, request(principal, "k", "body two", other, rec.render))
	if !errors.Is(err, claims.ErrIdempotencyKeyReused) {
		t.Fatalf("err = %v, want ErrIdempotencyKeyReused", err)
	}
	if n := countClaims(t, pool, principal); n != 1 {
		t.Errorf("%d claims, want 1", n)
	}
}

func TestCreateScopesKeysToTheCaller(t *testing.T) {
	ctx := context.Background()
	store := claims.NewStore(connect(t))
	rec := &recorder{}

	a, err := store.Create(ctx, request(newPrincipal(), "shared-key", "body", draft(), rec.render))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(ctx, request(newPrincipal(), "shared-key", "body", draft(), rec.render))
	if err != nil {
		t.Fatal(err)
	}
	if b.Replayed || a.ClaimID == b.ClaimID {
		t.Fatalf("two callers' requests with one key gave %+v and %+v, want two claims", a, b)
	}
}

func TestCreateReportsTheKeyInUseWhileTheFirstRequestRuns(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	store := claims.NewStore(pool)
	principal := newPrincipal()

	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	slow := func(c claims.Claim) (claims.Response, error) {
		close(entered)
		<-release
		return claims.Response{Status: 201, Body: []byte(`{}`)}, nil
	}

	type outcome struct {
		res claims.CreateResult
		err error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		res, err := store.Create(ctx, request(principal, "k", "body", draft(), slow))
		firstDone <- outcome{res, err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first request never reached Render")
	}

	_, err := store.Create(ctx, request(principal, "k", "body", draft(), (&recorder{}).render))
	releaseOnce.Do(func() { close(release) })
	if !errors.Is(err, claims.ErrIdempotencyKeyInUse) {
		t.Fatalf("concurrent duplicate: err = %v, want ErrIdempotencyKeyInUse", err)
	}
	first := <-firstDone
	if first.err != nil {
		t.Fatalf("first request: %v", first.err)
	}
	if n := countClaims(t, pool, principal); n != 1 {
		t.Errorf("%d claims, want 1", n)
	}
}

func TestCreateRejectsAnUnknownMember(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	principal := newPrincipal()
	d := draft()
	d.MemberID = uuid.MustParse("01930000-0000-7000-8000-00000000dead")

	_, err := claims.NewStore(pool).Create(ctx, request(principal, "k", "body", d, (&recorder{}).render))
	if !errors.Is(err, claims.ErrMemberNotFound) {
		t.Fatalf("err = %v, want ErrMemberNotFound", err)
	}
	if n := countClaims(t, pool, principal); n != 0 {
		t.Errorf("%d claims, want 0", n)
	}
}

func TestCreateNeedsAPolicyYearCoveringTheAdmission(t *testing.T) {
	ctx := context.Background()
	store := claims.NewStore(connect(t))
	for _, tc := range []struct {
		admitted string
		wantErr  error
	}{
		{"2026-03-31", claims.ErrNoPolicyYear}, // the day before the policy year
		{"2026-04-01", nil},                    // its first day
		{"2027-03-31", nil},                    // its last day
		{"2027-04-01", claims.ErrNoPolicyYear}, // the day after
	} {
		t.Run(tc.admitted, func(t *testing.T) {
			d := draft()
			d.AdmittedOn = day(tc.admitted)
			d.DischargedOn = day(tc.admitted)
			_, err := store.Create(ctx, request(newPrincipal(), "k", tc.admitted, d, (&recorder{}).render))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCreateTreatsAnExpiredKeyAsNew(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	principal := newPrincipal()
	oldHash := sha256.Sum256([]byte("an old request"))
	if _, err := pool.Exec(ctx, `
		INSERT INTO idempotency_keys (principal, idempotency_key, request_hash, response_status,
		                              response_body, created_at, expires_at)
		VALUES ($1, 'k', $2, 201, '{}', now() - interval '2 days', now() - interval '1 day')`,
		principal, oldHash[:]); err != nil {
		t.Fatalf("inserting an expired key: %v", err)
	}

	res, err := claims.NewStore(pool).Create(ctx, request(principal, "k", "a new request", draft(), (&recorder{}).render))
	if err != nil {
		t.Fatalf("Create with an expired key: %v", err)
	}
	if res.Replayed {
		t.Fatal("an expired key was replayed")
	}
	var hash []byte
	if err := pool.QueryRow(ctx, `SELECT request_hash FROM idempotency_keys WHERE principal = $1 AND idempotency_key = 'k'`,
		principal).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	newHash := sha256.Sum256([]byte("a new request"))
	if string(hash) != string(newHash[:]) {
		t.Error("the key still holds the expired request's hash")
	}
}

func TestGetReturnsTheStoredClaim(t *testing.T) {
	ctx := context.Background()
	store := claims.NewStore(connect(t))
	principal := newPrincipal()
	res, err := store.Create(ctx, request(principal, "k", "body", draft(), (&recorder{}).render))
	if err != nil {
		t.Fatal(err)
	}

	c, err := store.Get(ctx, res.ClaimID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c.ID != res.ClaimID || c.MemberID != seededMember || c.Status != claims.StatusDraft ||
		!c.AdmittedOn.Equal(day("2026-10-01")) || !c.DischargedOn.Equal(day("2026-10-05")) ||
		c.HospitalName != "Synthetic Test Hospital" || c.ClaimedAmountPaise != 12500000 ||
		c.WorkflowID != "claim-"+res.ClaimID.String() || c.CreatedBy != principal || c.Version != 1 ||
		c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() || c.SubmittedAt != nil || c.DecidedAt != nil {
		t.Errorf("Get = %+v", c)
	}
}

func TestGetReportsAnUnknownClaim(t *testing.T) {
	_, err := claims.NewStore(connect(t)).Get(context.Background(), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, claims.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDocumentsListsOnlyTheClaimsDocumentsOldestFirst(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	store := claims.NewStore(pool)
	a, err := store.Create(ctx, request(newPrincipal(), "k", "a", draft(), (&recorder{}).render))
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.Create(ctx, request(newPrincipal(), "k", "b", draft(), (&recorder{}).render))
	if err != nil {
		t.Fatal(err)
	}
	insert := func(claim uuid.UUID, file string, sum byte, uploaded bool, createdAt string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		status, pages, uploadedAt := "pending_upload", (*int)(nil), (*time.Time)(nil)
		if uploaded {
			n, at := 3, time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)
			status, pages, uploadedAt = "uploaded", &n, &at
		}
		hash := make([]byte, 32)
		hash[0] = sum
		if _, err := pool.Exec(ctx, `
			INSERT INTO documents (id, claim_id, status, file_name, content_type, size_bytes, sha256,
			                       object_key, page_count, created_at, uploaded_at)
			VALUES ($1, $2, $3, $4, 'application/pdf', 2048, $5, $6, $7, $8, $9)`,
			id, claim, status, file, hash, "test/"+id.String(), pages, createdAt, uploadedAt); err != nil {
			t.Fatalf("inserting a document: %v", err)
		}
		return id
	}
	later := insert(a.ClaimID, "bill.pdf", 1, true, "2026-10-06T09:00:00Z")
	earlier := insert(a.ClaimID, "discharge-summary.pdf", 2, false, "2026-10-06T08:00:00Z")
	insert(b.ClaimID, "other-claim.pdf", 3, false, "2026-10-06T07:00:00Z")

	docs, err := store.Documents(ctx, a.ClaimID)
	if err != nil {
		t.Fatalf("Documents: %v", err)
	}
	if len(docs) != 2 || docs[0].ID != earlier || docs[1].ID != later {
		t.Fatalf("Documents = %+v, want [%s %s]", docs, earlier, later)
	}
	d := docs[1]
	if d.ClaimID != a.ClaimID || d.Status != "uploaded" || d.FileName != "bill.pdf" ||
		d.ContentType != "application/pdf" || d.SizeBytes != 2048 || d.SHA256[0] != 1 || len(d.SHA256) != 32 ||
		d.PageCount == nil || *d.PageCount != 3 || d.RejectionReason != nil ||
		d.UploadedAt == nil || !d.UploadedAt.Equal(time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)) {
		t.Errorf("uploaded document = %+v", d)
	}
	if p := docs[0]; p.Status != "pending_upload" || p.PageCount != nil || p.UploadedAt != nil {
		t.Errorf("pending document = %+v", p)
	}
}
