package relay_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/relay"
)

// The seed's synthetic member, covered from 2026-04-01 to 2027-03-31 (db/seed/dev.sql).
var seededMember = uuid.MustParse("01930000-0000-7000-8000-000000000201")

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

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

// isolated begins a transaction that is rolled back when the test ends, so nothing the
// test does outlasts it. The relay delivers the oldest due row in the whole outbox, and
// the test database is shared: earlier runs and other packages' tests leave unpublished
// rows. Inside this transaction only, those rows are marked published, so the relay sees
// just the rows the test makes, plus those of the claims in keep.
func isolated(t *testing.T, pool *pgxpool.Pool, keep ...uuid.UUID) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if keep == nil {
		keep = []uuid.UUID{}
	}
	if _, err := tx.Exec(ctx, `UPDATE outbox SET published_at = now()
		WHERE published_at IS NULL AND NOT (claim_id = ANY($1))`, keep); err != nil {
		t.Fatalf("hiding the shared outbox's backlog: %v", err)
	}
	return tx
}

// newClaim creates a claim through the claims store, which queues its workflow start.
func newClaim(t *testing.T, db claims.DB) uuid.UUID {
	t.Helper()
	res, err := claims.NewStore(db).Create(context.Background(), claims.CreateRequest{
		Principal:      "test-" + uuid.NewString(),
		IdempotencyKey: "k",
		RequestHash:    sha256.Sum256([]byte(uuid.NewString())),
		Draft: claims.Draft{
			MemberID:           seededMember,
			AdmittedOn:         time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			DischargedOn:       time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC),
			HospitalName:       "Synthetic Relay Hospital",
			ClaimedAmountPaise: 100000,
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

func startEvent(t *testing.T, db claims.DB, claim uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRow(context.Background(),
		`SELECT event_id FROM outbox WHERE claim_id = $1 AND kind = 'workflow_start'`, claim).Scan(&id); err != nil {
		t.Fatalf("finding the start row: %v", err)
	}
	return id
}

// addSignal queues a signal for the claim, as the API will when a claim is submitted.
func addSignal(t *testing.T, tx pgx.Tx, claim uuid.UUID, name string) uuid.UUID {
	t.Helper()
	eventID := uuid.Must(uuid.NewV7())
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO outbox (event_id, claim_id, kind, workflow_id, name, payload)
		VALUES ($1, $2, 'workflow_signal', $3, $4, $5)`,
		eventID, claim, "claim-"+claim.String(), name, fmt.Sprintf(`{"claimId":%q}`, claim)); err != nil {
		t.Fatalf("queueing a signal: %v", err)
	}
	return eventID
}

type outboxRow struct {
	published bool
	attempts  int
	lastError *string
	dueIn     time.Duration // next_attempt_at minus now(), which is fixed inside a transaction
}

func readRow(t *testing.T, tx pgx.Tx, eventID uuid.UUID) outboxRow {
	t.Helper()
	var r outboxRow
	var dueMicros int64
	if err := tx.QueryRow(context.Background(), `
		SELECT published_at IS NOT NULL, attempts, last_error,
		       (extract(epoch FROM next_attempt_at - now()) * 1e6)::bigint
		FROM outbox WHERE event_id = $1`, eventID).Scan(&r.published, &r.attempts, &r.lastError, &dueMicros); err != nil {
		t.Fatalf("reading outbox row %s: %v", eventID, err)
	}
	r.dueIn = time.Duration(dueMicros) * time.Microsecond
	return r
}

func makeDue(t *testing.T, tx pgx.Tx, eventID uuid.UUID) {
	t.Helper()
	if _, err := tx.Exec(context.Background(), `UPDATE outbox SET next_attempt_at = now() WHERE event_id = $1`, eventID); err != nil {
		t.Fatal(err)
	}
}

// fake records every delivery attempt. It fails the events in fail with their error, and
// when hold is set, the first attempt waits until hold is closed (entered closes first).
type fake struct {
	mu       sync.Mutex
	attempts []relay.Message
	fail     map[uuid.UUID]error
	hold     chan struct{}
	entered  chan struct{}
	once     sync.Once
}

func (f *fake) Deliver(_ context.Context, m relay.Message) error {
	f.mu.Lock()
	f.attempts = append(f.attempts, m)
	err := f.fail[m.EventID]
	f.mu.Unlock()
	if f.hold != nil {
		f.once.Do(func() {
			close(f.entered)
			<-f.hold
		})
	}
	return err
}

func (f *fake) setFail(eventID uuid.UUID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail == nil {
		f.fail = map[uuid.UUID]error{}
	}
	f.fail[eventID] = err
}

func (f *fake) events() []uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uuid.UUID, 0, len(f.attempts))
	for _, m := range f.attempts {
		out = append(out, m.EventID)
	}
	return out
}

func deliverNext(t *testing.T, r *relay.Relay) bool {
	t.Helper()
	handled, err := r.DeliverNext(context.Background())
	if err != nil {
		t.Fatalf("DeliverNext: %v", err)
	}
	return handled
}

func TestDeliverNextDeliversAStartAndMarksItPublished(t *testing.T) {
	tx := isolated(t, connect(t))
	claim := newClaim(t, tx)
	start := startEvent(t, tx, claim)
	f := &fake{}
	r := relay.New(tx, f, quiet)

	if !deliverNext(t, r) {
		t.Fatal("DeliverNext found nothing to deliver")
	}
	if len(f.attempts) != 1 {
		t.Fatalf("%d deliveries, want 1", len(f.attempts))
	}
	m := f.attempts[0]
	var payload map[string]string
	if err := json.Unmarshal(m.Payload, &payload); err != nil {
		t.Fatalf("payload %s: %v", m.Payload, err)
	}
	if m.EventID != start || m.ClaimID != claim || m.Kind != relay.KindWorkflowStart ||
		m.WorkflowID != "claim-"+claim.String() || m.Name != "ClaimWorkflow" ||
		!reflect.DeepEqual(payload, map[string]string{"claimId": claim.String()}) {
		t.Errorf("delivered %+v (payload %s)", m, m.Payload)
	}
	if row := readRow(t, tx, start); !row.published || row.attempts != 0 || row.lastError != nil {
		t.Errorf("after delivery the row is %+v, want published with no attempts or error", row)
	}
	if deliverNext(t, r) {
		t.Errorf("a published row was delivered again: %v", f.events())
	}
}

func TestDeliverNextReportsWhenNothingIsDue(t *testing.T) {
	tx := isolated(t, connect(t))
	f := &fake{}
	if deliverNext(t, relay.New(tx, f, quiet)) || len(f.attempts) != 0 {
		t.Fatalf("an empty outbox gave deliveries %v", f.events())
	}
}

func TestDeliverNextKeepsAClaimsMessagesInOrder(t *testing.T) {
	tx := isolated(t, connect(t))
	claim := newClaim(t, tx)
	start := startEvent(t, tx, claim)
	submitted := addSignal(t, tx, claim, "submitted")
	f := &fake{}
	f.setFail(start, errors.New("temporal unavailable"))
	r := relay.New(tx, f, quiet)

	deliverNext(t, r) // the start fails and waits for its retry
	if deliverNext(t, r) {
		t.Fatalf("the signal went out while the claim's start was still undelivered: %v", f.events())
	}

	f.setFail(start, nil)
	makeDue(t, tx, start)
	for deliverNext(t, r) {
	}
	if got, want := f.events(), []uuid.UUID{start, start, submitted}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attempts %v, want start (failed), start, submitted %v", got, want)
	}
}

func TestDeliverNextDoesNotHoldUpOtherClaims(t *testing.T) {
	tx := isolated(t, connect(t))
	stuck := startEvent(t, tx, newClaim(t, tx))
	other := startEvent(t, tx, newClaim(t, tx))
	f := &fake{}
	f.setFail(stuck, errors.New("temporal unavailable"))
	r := relay.New(tx, f, quiet)

	for deliverNext(t, r) {
	}
	if got, want := f.events(), []uuid.UUID{stuck, other}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attempts %v, want %v", got, want)
	}
	if row := readRow(t, tx, other); !row.published {
		t.Error("another claim's message waited behind a failing one")
	}
}

func TestDeliverNextBacksOffAfterAFailure(t *testing.T) {
	tx := isolated(t, connect(t))
	start := startEvent(t, tx, newClaim(t, tx))
	f := &fake{}
	f.setFail(start, errors.New("dial tcp temporal:7233: connection refused"))
	r := relay.New(tx, f, quiet)

	deliverNext(t, r)
	first := readRow(t, tx, start)
	if first.published || first.attempts != 1 || first.dueIn <= 0 ||
		first.lastError == nil || *first.lastError != "dial tcp temporal:7233: connection refused" {
		t.Fatalf("after one failure the row is %+v, want unpublished, 1 attempt, due later, with the error", first)
	}

	makeDue(t, tx, start)
	deliverNext(t, r)
	if second := readRow(t, tx, start); second.attempts != 2 || second.dueIn <= first.dueIn {
		t.Fatalf("after a second failure the row is %+v; want 2 attempts and a longer wait than %v", second, first.dueIn)
	}

	if _, err := tx.Exec(context.Background(), `UPDATE outbox SET attempts = 40, next_attempt_at = now() WHERE event_id = $1`, start); err != nil {
		t.Fatal(err)
	}
	deliverNext(t, r)
	if many := readRow(t, tx, start); many.attempts != 41 || many.dueIn <= 0 || many.dueIn > 10*time.Minute {
		t.Fatalf("after 41 failures the row is %+v, want a wait capped within minutes", many)
	}
}

func TestDeliverNextStopsRetryingAnUndeliverableMessage(t *testing.T) {
	tx := isolated(t, connect(t))
	claim := newClaim(t, tx)
	start := startEvent(t, tx, claim)
	late := addSignal(t, tx, claim, "submitted")
	f := &fake{}
	f.setFail(late, fmt.Errorf("%w: workflow claim-x already closed", relay.ErrUndeliverable))
	r := relay.New(tx, f, quiet)

	for deliverNext(t, r) {
	}
	row := readRow(t, tx, late)
	if !row.published || row.lastError == nil || *row.lastError == "" {
		t.Fatalf("the undeliverable signal's row is %+v, want published with the reason recorded", row)
	}
	if got, want := f.events(), []uuid.UUID{start, late}; !reflect.DeepEqual(got, want) {
		t.Fatalf("attempts %v, want each message once: %v", got, want)
	}
}

func TestTwoRelaysNeverDeliverTheSameMessageAtOnce(t *testing.T) {
	pool := connect(t)
	// Both relays must see these claims, so they are committed. Their rows stay behind,
	// as every test claim does.
	first, second := newClaim(t, pool), newClaim(t, pool)
	firstStart, secondStart := startEvent(t, pool, first), startEvent(t, pool, second)

	// Relay A takes the first claim's start, the oldest row it can see, and holds it.
	txA := isolated(t, pool, first, second)
	a := &fake{hold: make(chan struct{}), entered: make(chan struct{})}
	defer func() {
		select {
		case <-a.hold:
		default:
			close(a.hold)
		}
	}()
	aDone := make(chan error, 1)
	go func() {
		_, err := relay.New(txA, a, quiet).DeliverNext(context.Background())
		aDone <- err
	}()
	select {
	case <-a.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("relay A never started a delivery")
	}
	if got := a.events(); len(got) != 1 || got[0] != firstStart {
		t.Fatalf("relay A took %v, want the first claim's start %s", got, firstStart)
	}

	// Relay B, in its own transaction, must skip that row while A holds it.
	txB, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = txB.Rollback(context.Background()) }()
	b := &fake{}
	rb := relay.New(txB, b, quiet)
	for i := 0; i < 1000 && !contains(b.events(), secondStart); i++ {
		if !deliverNext(t, rb) {
			break
		}
	}
	close(a.hold)
	if err := <-aDone; err != nil {
		t.Fatalf("relay A: %v", err)
	}
	if contains(b.events(), firstStart) {
		t.Fatal("relay B delivered the message relay A was still delivering")
	}
	if !contains(b.events(), secondStart) {
		t.Fatalf("relay B didn't deliver the other claim's start; it delivered %v", b.events())
	}
}

func contains(ids []uuid.UUID, id uuid.UUID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func TestRunDeliversUntilStopped(t *testing.T) {
	tx := isolated(t, connect(t))
	claim := newClaim(t, tx)
	start := startEvent(t, tx, claim)
	submitted := addSignal(t, tx, claim, "submitted")

	delivered := make(chan struct{})
	f := &notifyingFake{want: submitted, done: delivered}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- relay.New(tx, f, quiet).Run(ctx, 10*time.Millisecond) }()

	select {
	case <-delivered:
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("Run didn't deliver the queued messages")
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned %v after being stopped", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run didn't stop")
	}
	for _, id := range []uuid.UUID{start, submitted} {
		if !readRow(t, tx, id).published {
			t.Errorf("row %s wasn't published", id)
		}
	}
}

// notifyingFake delivers everything and closes done when it has delivered want.
type notifyingFake struct {
	want uuid.UUID
	done chan struct{}
	once sync.Once
}

func (f *notifyingFake) Deliver(_ context.Context, m relay.Message) error {
	if m.EventID == f.want {
		f.once.Do(func() { close(f.done) })
	}
	return nil
}
