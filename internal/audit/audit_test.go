package audit_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/audit"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

var claimID = uuid.MustParse("01930000-0000-7000-8000-0000000000c1")

// sample is an event with every kind of field set. Its time has nanoseconds, which the
// encoding cuts to microseconds.
func sample() audit.Event {
	return audit.Event{
		ClaimID:    claimID,
		Seq:        2,
		OccurredAt: time.Date(2026, 11, 24, 6, 41, 21, 468856789, time.UTC),
		Actor:      audit.Actor{Type: audit.ActorUser, ID: "test-submitter"},
		Action:     audit.ActionDocumentRegistered,
		Details: audit.Details{
			"sizeBytes":   int64(2048),
			"documentId":  "01930000-0000-7000-8000-0000000000d1",
			"contentType": "application/pdf",
			"code":        "<a&b>",
		},
	}
}

// sampleCanonical is sample's canonical encoding, written out by hand: keys sorted at both
// levels, no HTML escaping, the time in UTC to the microsecond.
const sampleCanonical = `{"action":"document.registered","actorId":"test-submitter","actorType":"user",` +
	`"claimId":"01930000-0000-7000-8000-0000000000c1","details":{"code":"<a&b>",` +
	`"contentType":"application/pdf","documentId":"01930000-0000-7000-8000-0000000000d1",` +
	`"sizeBytes":2048},"occurredAt":"2026-11-24T06:41:21.468856Z","seq":2}`

func TestCanonicalEncodingIsSortedCompactJSON(t *testing.T) {
	got, err := audit.Canonical(sample())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sampleCanonical {
		t.Fatalf("Canonical =\n%s\nwant\n%s", got, sampleCanonical)
	}
}

func TestHashIsSHA256OverThePreviousHashAndTheCanonicalEncoding(t *testing.T) {
	prev := bytes.Repeat([]byte{0xab}, 32)
	tests := []struct {
		name string
		prev []byte
		want [32]byte
	}{
		{"a later event", prev, sha256.Sum256(append(append([]byte{}, prev...), sampleCanonical...))},
		{"a claim's first event", nil, sha256.Sum256(append(make([]byte, 32), sampleCanonical...))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := audit.Hash(tc.prev, sample())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("Hash = %x, want %x", got, tc.want)
			}
		})
	}
}

func TestHashChangesWithEveryField(t *testing.T) {
	prev := bytes.Repeat([]byte{1}, 32)
	base, err := audit.Hash(prev, sample())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		prev   []byte
		change func(*audit.Event)
	}{
		{"previous hash", bytes.Repeat([]byte{2}, 32), func(*audit.Event) {}},
		{"claim", prev, func(e *audit.Event) { e.ClaimID = uuid.MustParse("01930000-0000-7000-8000-0000000000c2") }},
		{"sequence", prev, func(e *audit.Event) { e.Seq = 3 }},
		{"time, by a microsecond", prev, func(e *audit.Event) { e.OccurredAt = e.OccurredAt.Add(time.Microsecond) }},
		{"actor type", prev, func(e *audit.Event) { e.Actor.Type = audit.ActorService }},
		{"actor", prev, func(e *audit.Event) { e.Actor.ID = "test-other" }},
		{"action", prev, func(e *audit.Event) { e.Action = audit.ActionDocumentUploaded }},
		{"a detail's value", prev, func(e *audit.Event) { e.Details["sizeBytes"] = int64(2049) }},
		{"a detail's type", prev, func(e *audit.Event) { e.Details["sizeBytes"] = "2048" }},
		{"a detail's key", prev, func(e *audit.Event) {
			e.Details["bytes"] = e.Details["sizeBytes"]
			delete(e.Details, "sizeBytes")
		}},
		{"a detail added", prev, func(e *audit.Event) { e.Details["reason"] = "size_mismatch" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := sample()
			tc.change(&e)
			got, err := audit.Hash(tc.prev, e)
			if err != nil {
				t.Fatal(err)
			}
			if got == base {
				t.Fatal("the hash didn't change")
			}
		})
	}
}

func TestHashIgnoresTheTimeZoneSubMicrosecondsAndIntegerWidth(t *testing.T) {
	base, err := audit.Hash(nil, sample())
	if err != nil {
		t.Fatal(err)
	}
	ist := time.FixedZone("IST", 5*3600+1800)
	tests := []struct {
		name   string
		change func(*audit.Event)
	}{
		{"the same instant in IST", func(e *audit.Event) { e.OccurredAt = e.OccurredAt.In(ist) }},
		{"a different nanosecond in the same microsecond", func(e *audit.Event) {
			e.OccurredAt = e.OccurredAt.Truncate(time.Microsecond).Add(999 * time.Nanosecond)
		}},
		{"an int", func(e *audit.Event) { e.Details["sizeBytes"] = 2048 }},
		{"an int32", func(e *audit.Event) { e.Details["sizeBytes"] = int32(2048) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := sample()
			tc.change(&e)
			got, err := audit.Hash(nil, e)
			if err != nil {
				t.Fatal(err)
			}
			if got != base {
				t.Fatal("the hash changed")
			}
		})
	}
}

func TestDetailsHoldOnlyStringsAndIntegers(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value any
	}{
		{"a float", "amount", 12.5},
		{"a bool", "ok", true},
		{"a nil", "none", nil},
		{"bytes", "sha256", []byte{1, 2}},
		{"a nested object", "doc", map[string]any{"id": "x"}},
		{"an unsigned integer", "size", uint64(1)},
		{"invalid UTF-8", "name", "\xff\xfe"},
		{"a NUL", "code", "a\x00b"},
		{"an empty key", "", "x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := sample()
			e.Details[tc.key] = tc.value
			if _, err := audit.Hash(nil, e); err == nil {
				t.Fatal("Hash accepted it")
			}
		})
	}
}

func TestHashRejectsAPreviousHashOfTheWrongLength(t *testing.T) {
	if _, err := audit.Hash(make([]byte, 31), sample()); err == nil {
		t.Fatal("Hash accepted a 31-byte previous hash")
	}
}

// chain builds a valid chain of n events in memory.
func chain(t *testing.T, n int32) []audit.Record {
	t.Helper()
	var (
		out  []audit.Record
		prev []byte
	)
	for seq := int32(1); seq <= n; seq++ {
		e := sample()
		e.Seq = seq
		e.OccurredAt = e.OccurredAt.Add(time.Duration(seq) * time.Second).Truncate(time.Microsecond)
		sum, err := audit.Hash(prev, e)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, audit.Record{Event: e, PrevHash: prev, Hash: sum[:]})
		prev = sum[:]
	}
	return out
}

func TestVerifyFindsEveryKindOfBreak(t *testing.T) {
	if err := audit.Verify(chain(t, 3)); err != nil {
		t.Fatalf("Verify on a valid chain: %v", err)
	}
	tests := []struct {
		name   string
		change func([]audit.Record) []audit.Record
	}{
		{"a changed detail", func(r []audit.Record) []audit.Record {
			r[1].Details = audit.Details{"sizeBytes": int64(1)}
			return r
		}},
		{"a changed time", func(r []audit.Record) []audit.Record {
			r[2].OccurredAt = r[2].OccurredAt.Add(time.Microsecond)
			return r
		}},
		{"a wrong link", func(r []audit.Record) []audit.Record {
			r[2].PrevHash = r[0].Hash
			return r
		}},
		{"a gap", func(r []audit.Record) []audit.Record { return append(r[:1], r[2:]...) }},
		{"a first event with a previous hash", func(r []audit.Record) []audit.Record {
			r[0].PrevHash = make([]byte, 32)
			return r
		}},
		{"another claim's event", func(r []audit.Record) []audit.Record {
			r[1].ClaimID = uuid.MustParse("01930000-0000-7000-8000-0000000000c2")
			return r
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := audit.Verify(tc.change(chain(t, 3))); err == nil {
				t.Fatal("Verify passed a broken chain")
			}
		})
	}
}

// connect opens a pool on TEST_DATABASE_URL, the migrated and seeded database, as
// claims_app. Each test makes its own claim, so tests never share a chain.
func connect(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; this test needs the migrated, seeded database")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// newClaim creates a claim through the claims store, which writes its claim.created event.
func newClaim(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	res, err := claims.NewStore(pool).Create(context.Background(), claims.CreateRequest{
		Principal:      "test-" + uuid.NewString(),
		IdempotencyKey: "k",
		RequestHash:    sha256.Sum256([]byte("audit test")),
		Draft: claims.Draft{
			MemberID:           uuid.MustParse("01930000-0000-7000-8000-000000000201"),
			AdmittedOn:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			DischargedOn:       time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
			HospitalName:       "Synthetic Test Hospital",
			ClaimedAmountPaise: 12500000,
		},
		Render: func(claims.Claim) (claims.Response, error) {
			return claims.Response{Status: 201, Body: []byte(`{}`)}, nil
		},
	})
	if err != nil {
		t.Fatalf("creating a claim: %v", err)
	}
	return res.ClaimID
}

// appendLocked appends one event in its own transaction, after locking the claim's row as
// every writer must.
func appendLocked(ctx context.Context, pool *pgxpool.Pool, e audit.Entry) (audit.Record, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return audit.Record{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM claims WHERE id = $1 FOR UPDATE`, e.ClaimID); err != nil {
		return audit.Record{}, err
	}
	r, err := audit.Append(ctx, tx, e)
	if err != nil {
		return audit.Record{}, err
	}
	return r, tx.Commit(ctx)
}

func TestAppendedEventsRecomputeFromTheStoredRows(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	id := newClaim(t, pool)

	// Beyond 2^53, so a detail that passed through a float64 would come back changed.
	const big = int64(9007199254740993)
	at := time.Date(2026, 11, 24, 6, 41, 21, 468856789, time.FixedZone("IST", 5*3600+1800))
	appended, err := appendLocked(ctx, pool, audit.Entry{
		ClaimID: id, At: at,
		Actor:   audit.Actor{Type: audit.ActorService, ID: "audit-test"},
		Action:  "test.checked",
		Details: audit.Details{"amountPaise": big, "code": "ok", "count": 3},
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := appendLocked(ctx, pool, audit.Entry{
		ClaimID: id, At: at.Add(time.Second),
		Actor:  audit.Actor{Type: audit.ActorModel, ID: "audit-test"},
		Action: "test.checked_again",
	}); err != nil {
		t.Fatalf("second Append: %v", err)
	}

	records, err := audit.List(ctx, pool, id)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := audit.Verify(records); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("%d events, want 3", len(records))
	}
	first, second := records[0], records[1]
	if first.Action != audit.ActionClaimCreated || first.Actor.Type != audit.ActorUser ||
		first.Details["claimedAmountPaise"] != int64(12500000) ||
		first.Details["memberId"] != "01930000-0000-7000-8000-000000000201" {
		t.Errorf("first event = %+v, want claim.created", first)
	}
	if !second.OccurredAt.Equal(at.Truncate(time.Microsecond)) {
		t.Errorf("stored time = %v, want %v", second.OccurredAt, at.Truncate(time.Microsecond))
	}
	if second.Details["amountPaise"] != big || second.Details["count"] != int64(3) || second.Details["code"] != "ok" {
		t.Errorf("stored details = %#v", second.Details)
	}
	if !bytes.Equal(second.Hash, appended.Hash) || second.Seq != appended.Seq {
		t.Errorf("stored event %d hash %x, Append returned %d %x", second.Seq, second.Hash, appended.Seq, appended.Hash)
	}
}

func TestConcurrentAppendsUnderTheClaimLockMakeOneChain(t *testing.T) {
	ctx := context.Background()
	pool := connect(t)
	id := newClaim(t, pool)

	const writers = 10
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := appendLocked(ctx, pool, audit.Entry{
				ClaimID: id, At: time.Now(),
				Actor:   audit.Actor{Type: audit.ActorService, ID: "audit-test"},
				Action:  "test.concurrent",
				Details: audit.Details{"writer": i},
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("Append: %v", err)
		}
	}
	records, err := audit.List(ctx, pool, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != writers+1 {
		t.Fatalf("%d events, want %d", len(records), writers+1)
	}
	if err := audit.Verify(records); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
