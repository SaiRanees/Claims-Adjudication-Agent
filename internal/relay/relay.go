package relay

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The kinds of outbox message, as the outbox table's CHECK constraint lists them.
const (
	KindWorkflowStart  = "workflow_start"
	KindWorkflowSignal = "workflow_signal"
)

const (
	// callTimeout bounds one delivery. The message's row stays locked, in an open
	// transaction, while Temporal is called, so this must stay well under claims_app's
	// idle_in_transaction_session_timeout of 30 seconds.
	callTimeout = 10 * time.Second
	// recordTimeout bounds recording an outcome, which happens even during shutdown.
	recordTimeout = 5 * time.Second

	firstBackoff = time.Second
	maxBackoff   = 5 * time.Minute
	maxErrorLen  = 1000
)

// Message is one outbox row: a workflow start or a signal for one claim.
type Message struct {
	ID         int64
	EventID    uuid.UUID
	ClaimID    uuid.UUID
	Kind       string // KindWorkflowStart or KindWorkflowSignal
	WorkflowID string // claim-<claim id>
	Name       string // the workflow type to start, or the signal to send
	Payload    []byte // a JSON object of IDs (ADR-0004)
	Attempts   int    // failed deliveries so far
}

// Deliverer hands a message to its workflow. A message reaches Temporal at least once
// (ADR-0003), so delivering one twice must be harmless.
type Deliverer interface {
	Deliver(ctx context.Context, m Message) error
}

// ErrUndeliverable wraps the reason a message can never be delivered, such as a signal
// for a workflow that has already closed. The relay records the reason and stops
// retrying; any other error is retried with backoff.
var ErrUndeliverable = errors.New("the message can't be delivered")

// DB is the part of a pgx pool the relay uses. A pgx.Tx satisfies it too.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Relay delivers the outbox to Temporal (ADR-0003). Several relays can run at once: each
// message is locked while it is delivered, and a claim's messages go out in order.
type Relay struct {
	db        DB
	deliverer Deliverer
	log       *slog.Logger
}

// New returns a Relay that reads the outbox from db and delivers through d.
func New(db DB, d Deliverer, log *slog.Logger) *Relay {
	return &Relay{db: db, deliverer: d, log: log}
}

// DeliverNext delivers the next due message in its own transaction. It reports whether it
// handled one, whether delivered, given up on, or rescheduled after a failure; false
// means nothing is due.
func (r *Relay) DeliverNext(ctx context.Context) (bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning a delivery: %w", err)
	}
	// A no-op once the transaction has committed.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	m, err := next(ctx, tx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reading the outbox: %w", err)
	}

	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	deliverErr := r.deliverer.Deliver(callCtx, m)
	cancel()
	if deliverErr != nil && ctx.Err() != nil {
		// Shutting down, which is no fault of the message: leave its attempts alone.
		// The rollback unlocks it for the next relay.
		return false, ctx.Err()
	}

	// Record the outcome even if shutdown has begun: a delivery that is made but not
	// recorded is made again.
	rec, cancelRec := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancelRec()
	switch {
	case deliverErr == nil:
		_, err = tx.Exec(rec, `UPDATE outbox SET published_at = now(), last_error = NULL WHERE id = $1`, m.ID)
	case errors.Is(deliverErr, ErrUndeliverable):
		r.log.Warn("giving up on an outbox message", "eventId", m.EventID, "claimId", m.ClaimID,
			"kind", m.Kind, "name", m.Name, "error", deliverErr)
		_, err = tx.Exec(rec, `UPDATE outbox SET published_at = now(), last_error = $2 WHERE id = $1`,
			m.ID, truncate(deliverErr.Error()))
	default:
		wait := backoff(m.Attempts + 1)
		r.log.Warn("delivering an outbox message failed; will retry", "eventId", m.EventID, "claimId", m.ClaimID,
			"kind", m.Kind, "name", m.Name, "attempt", m.Attempts+1, "retryIn", wait.String(), "error", deliverErr)
		_, err = tx.Exec(rec, `
			UPDATE outbox SET attempts = attempts + 1, next_attempt_at = now() + $2::interval, last_error = $3
			WHERE id = $1`, m.ID, wait, truncate(deliverErr.Error()))
	}
	if err != nil {
		return false, fmt.Errorf("recording the delivery of outbox row %d: %w", m.ID, err)
	}
	if err := tx.Commit(rec); err != nil {
		return false, fmt.Errorf("committing the delivery of outbox row %d: %w", m.ID, err)
	}
	return true, nil
}

// next locks the oldest message that is due and is the oldest undelivered message of its
// claim, so a claim's messages reach its workflow in order. Rows another relay holds
// are skipped, and a claim whose earlier message is waiting to be retried waits with it.
func next(ctx context.Context, tx pgx.Tx) (Message, error) {
	var m Message
	err := tx.QueryRow(ctx, `
		SELECT o.id, o.event_id, o.claim_id, o.kind, o.workflow_id, o.name, o.payload, o.attempts
		FROM outbox o
		WHERE o.published_at IS NULL
		  AND o.next_attempt_at <= now()
		  AND NOT EXISTS (
		      SELECT 1 FROM outbox earlier
		      WHERE earlier.claim_id = o.claim_id
		        AND earlier.published_at IS NULL
		        AND earlier.id < o.id)
		ORDER BY o.id
		LIMIT 1
		FOR UPDATE OF o SKIP LOCKED`).
		Scan(&m.ID, &m.EventID, &m.ClaimID, &m.Kind, &m.WorkflowID, &m.Name, &m.Payload, &m.Attempts)
	return m, err
}

// Run delivers due messages until ctx ends. When nothing is due, or the database can't
// be reached, it waits for idle before looking again.
func (r *Relay) Run(ctx context.Context, idle time.Duration) error {
	for {
		handled, err := r.DeliverNext(ctx)
		select {
		case <-ctx.Done():
			// Stopping is not a failure, and any error now is the cancellation itself.
			return nil
		default:
		}
		if err != nil {
			r.log.Error("delivering the outbox", "error", err)
		}
		if handled && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(idle):
		}
	}
}

// backoff is the wait before retry number attempt: one second, doubling, at most five
// minutes. The loop stops at the cap, so a large attempt count can't overflow.
func backoff(attempt int) time.Duration {
	wait := firstBackoff
	for i := 1; i < attempt && wait < maxBackoff; i++ {
		wait *= 2
	}
	return min(wait, maxBackoff)
}

// truncate shortens an error for the last_error column without splitting a character.
func truncate(s string) string {
	if len(s) <= maxErrorLen {
		return s
	}
	return strings.ToValidUTF8(s[:maxErrorLen], "")
}
