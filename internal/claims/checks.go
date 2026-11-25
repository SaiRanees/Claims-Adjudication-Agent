package claims

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/audit"
)

// CheckerActor is the audit actor for the worker's file checks.
var CheckerActor = audit.Actor{Type: audit.ActorService, ID: "worker"}

// Verdict is the worker's finding on one uploaded file: accepted with its page count and
// the SHA-256 of the PDF later steps read, or rejected for a reason.
type Verdict struct {
	// Reason is nil for an accepted file.
	Reason    *RejectionReason
	PageCount int32
	PDFSHA256 []byte
}

// ErrNotUploaded: the file isn't uploaded yet, so there's nothing to check.
var ErrNotUploaded = errors.New("the document is not uploaded")

// DocumentForCheck reads a file for the worker, which acts for no user.
func (s *Store) DocumentForCheck(ctx context.Context, claimID, documentID uuid.UUID) (Document, error) {
	d, err := scanDocument(s.db.QueryRow(ctx, `
		SELECT `+documentColumns+` FROM documents WHERE id = $1 AND claim_id = $2`, documentID, claimID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrDocumentNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("reading document %s: %w", documentID, err)
	}
	return d, nil
}

// RecordCheck commits the worker's verdict on an uploaded file, with its audit event, under
// the claim's lock. A file already accepted or rejected is left as it is and returned, so a
// retried check gets the first one's result; a file not yet uploaded is ErrNotUploaded.
func (s *Store) RecordCheck(ctx context.Context, claimID, documentID uuid.UUID, v Verdict) (Document, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Document{}, fmt.Errorf("beginning the check's record: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	c, err := lock(ctx, tx, claimID)
	if err != nil {
		return Document{}, err
	}
	d, err := scanDocument(tx.QueryRow(ctx, `
		SELECT `+documentColumns+` FROM documents WHERE id = $1 AND claim_id = $2`, documentID, claimID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrDocumentNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("reading document %s: %w", documentID, err)
	}
	switch d.Status {
	case DocumentAccepted, DocumentRejected:
		return d, nil
	case DocumentPendingUpload:
		return Document{}, ErrNotUploaded
	case DocumentUploaded:
	}

	at := s.now().UTC().Truncate(time.Microsecond)
	event := audit.Entry{
		ClaimID: c.ID,
		At:      at,
		Actor:   CheckerActor,
		Details: audit.Details{"documentId": d.ID.String()},
	}
	if v.Reason == nil {
		d, err = scanDocument(tx.QueryRow(ctx, `
			UPDATE documents SET status = 'accepted', checked_at = $2, page_count = $3, pdf_sha256 = $4
			WHERE id = $1
			RETURNING `+documentColumns, d.ID, at, v.PageCount, v.PDFSHA256))
		if err != nil {
			return Document{}, fmt.Errorf("accepting document %s: %w", documentID, err)
		}
		event.Action = audit.ActionDocumentAccepted
		event.Details["pageCount"] = v.PageCount
	} else {
		d, err = scanDocument(tx.QueryRow(ctx, `
			UPDATE documents SET status = 'rejected', checked_at = $2, rejection_reason = $3
			WHERE id = $1
			RETURNING `+documentColumns, d.ID, at, string(*v.Reason)))
		if err != nil {
			return Document{}, fmt.Errorf("rejecting document %s: %w", documentID, err)
		}
		event.Action = audit.ActionDocumentRejected
		event.Details["reason"] = string(*v.Reason)
	}
	if _, err := audit.Append(ctx, tx, event); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("committing the check's record: %w", err)
	}
	return d, nil
}
