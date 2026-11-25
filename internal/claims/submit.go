package claims

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/audit"
)

// SignalSubmitted tells a claim's workflow that the claim is submitted. The payload is
// empty; the relay adds the event ID.
const SignalSubmitted = "submitted"

// Errors Submit returns for a claim that can't be submitted yet.
var (
	// ErrNoAcceptedDocuments: the claim has no accepted file.
	ErrNoAcceptedDocuments = errors.New("the claim has no accepted document")
	// ErrChecksPending: a file is uploaded and its check hasn't finished.
	ErrChecksPending = errors.New("a document's check hasn't finished")
)

// Submit moves the submitter's draft claim to submitted: the status, the submitted signal
// for the workflow and the claim.submitted audit event commit together, under the claim's
// lock. It needs at least one accepted file and none still uploaded, so no check is
// pending; files still pending_upload stay out of the packet. A claim already past draft
// is returned as it is, so submitting again gets the current state.
//
// Because complete takes the same lock and refuses a claim past draft, an upload and a
// submission can't both commit: whichever locks second sees the other's result.
func (s *Store) Submit(ctx context.Context, principal string, claimID uuid.UUID) (Claim, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Claim{}, fmt.Errorf("beginning the submission: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	c, err := lockOwned(ctx, tx, claimID, principal)
	if err != nil {
		return Claim{}, err
	}
	if c.Status != StatusDraft {
		return c, nil
	}

	var accepted, uploaded int32
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'accepted'), count(*) FILTER (WHERE status = 'uploaded')
		FROM documents WHERE claim_id = $1`, c.ID).Scan(&accepted, &uploaded); err != nil {
		return Claim{}, fmt.Errorf("counting claim %s's documents: %w", c.ID, err)
	}
	switch {
	case uploaded > 0:
		return Claim{}, ErrChecksPending
	case accepted == 0:
		return Claim{}, ErrNoAcceptedDocuments
	}

	at := s.now().UTC().Truncate(time.Microsecond)
	c, err = scanClaim(tx.QueryRow(ctx, `
		UPDATE claims SET status = 'submitted', submitted_at = $2
		WHERE id = $1 AND version = $3
		RETURNING `+claimColumns, c.ID, at, c.Version))
	if err != nil {
		return Claim{}, fmt.Errorf("submitting claim %s: %w", claimID, err)
	}
	if err := queue(ctx, tx, c, "workflow_signal", SignalSubmitted, struct{}{}); err != nil {
		return Claim{}, fmt.Errorf("queueing the %s signal: %w", SignalSubmitted, err)
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		ClaimID: c.ID,
		At:      at,
		Actor:   audit.Actor{Type: audit.ActorUser, ID: principal},
		Action:  audit.ActionClaimSubmitted,
		Details: audit.Details{"acceptedDocuments": accepted},
	}); err != nil {
		return Claim{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Claim{}, fmt.Errorf("committing the submission: %w", err)
	}
	return c, nil
}
