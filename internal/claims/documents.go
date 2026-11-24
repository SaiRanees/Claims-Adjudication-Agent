package claims

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/audit"
)

// DocumentStatus is a file's place in its checks. The documents table's CHECK list is the
// authority; these mirror it.
type DocumentStatus string

// The document statuses.
const (
	DocumentPendingUpload DocumentStatus = "pending_upload"
	DocumentUploaded      DocumentStatus = "uploaded"
	DocumentRejected      DocumentStatus = "rejected"
)

// RejectionReason says why a file was rejected. The documents table's CHECK list is the
// authority; these mirror it.
type RejectionReason string

// The rejection reasons. Completing an upload gives the first three; the worker's checks
// give the rest.
const (
	RejectMissingObject    RejectionReason = "missing_object"
	RejectSizeMismatch     RejectionReason = "size_mismatch"
	RejectChecksumMismatch RejectionReason = "checksum_mismatch"
	RejectUnreadable       RejectionReason = "unreadable"
	RejectEncrypted        RejectionReason = "encrypted"
	RejectTooManyPages     RejectionReason = "too_many_pages"
)

const (
	// MaxFilesPerClaim caps a claim's files, rejected ones included. It bounds the claim's
	// storage and its workflow's history.
	MaxFilesPerClaim = 50
	// SignalDocumentUploaded tells a claim's workflow that a file is uploaded and waits for
	// its check. The payload is {"documentId": …}; the relay adds the event ID.
	SignalDocumentUploaded = "documentUploaded"
)

// Errors that callers turn into client responses.
var (
	// ErrDocumentNotFound: the claim has no document with the ID.
	ErrDocumentNotFound = errors.New("no such document on this claim")
	// ErrNotAcceptingDocuments: the claim is past draft and awaiting_documents.
	ErrNotAcceptingDocuments = errors.New("the claim is past accepting documents")
	// ErrAlreadyUploaded: a file with this SHA-256 is already uploaded to the claim.
	ErrAlreadyUploaded = errors.New("this file is already uploaded to the claim")
	// ErrDeclaredDifferently: a file with this SHA-256 is registered with another size or type.
	ErrDeclaredDifferently = errors.New("this file is already registered on the claim with a different size or type")
	// ErrTooManyFiles: the claim already has MaxFilesPerClaim files.
	ErrTooManyFiles = errors.New("the claim already has the most files allowed")
)

// AcceptsDocuments reports whether files can be added to the claim: only while it's a draft
// or waiting for missing documents.
func (c Claim) AcceptsDocuments() bool {
	switch c.Status {
	case StatusDraft, StatusAwaitingDocuments:
		return true
	case StatusSubmitted, StatusValidating, StatusExtracting, StatusAdjudicating, StatusInReview,
		StatusDecided, StatusClosed, StatusRejected, StatusFailed:
		return false
	}
	return false
}

// NewDocument is a file a client is about to upload, as it declares it.
type NewDocument struct {
	// Principal is the caller, who must be the claim's submitter.
	Principal   string
	ClaimID     uuid.UUID
	FileName    string
	ContentType string
	SizeBytes   int64
	SHA256      [32]byte
}

// RegisterDocument records a file the claim's submitter is about to upload, while the claim
// takes documents and has fewer than MaxFilesPerClaim files, with a document.registered
// audit event. The file is stored under a key made from IDs only.
//
// Registering the same SHA-256, size and type again while that file is pending_upload
// returns it with created false, so a client whose response was lost can carry on. The
// same SHA-256 with another size or type is ErrDeclaredDifferently, and a file already
// uploaded is ErrAlreadyUploaded. A rejected file doesn't count: it can be registered again.
func (s *Store) RegisterDocument(ctx context.Context, nd NewDocument) (d Document, created bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Document{}, false, fmt.Errorf("beginning the registration: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	c, err := lockOwned(ctx, tx, nd.ClaimID, nd.Principal)
	if err != nil {
		return Document{}, false, err
	}
	if !c.AcceptsDocuments() {
		return Document{}, false, ErrNotAcceptingDocuments
	}

	existing, err := scanDocument(tx.QueryRow(ctx, `
		SELECT `+documentColumns+`
		FROM documents
		WHERE claim_id = $1 AND sha256 = $2 AND status <> 'rejected'`, c.ID, nd.SHA256[:]))
	switch {
	case err == nil:
		return existingRegistration(existing, nd)
	case !errors.Is(err, pgx.ErrNoRows):
		return Document{}, false, fmt.Errorf("looking for the same file on claim %s: %w", c.ID, err)
	}

	var files int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM documents WHERE claim_id = $1`, c.ID).Scan(&files); err != nil {
		return Document{}, false, fmt.Errorf("counting claim %s's files: %w", c.ID, err)
	}
	if files >= MaxFilesPerClaim {
		return Document{}, false, ErrTooManyFiles
	}

	id, err := uuid.NewV7()
	if err != nil {
		return Document{}, false, fmt.Errorf("generating a document ID: %w", err)
	}
	d, err = scanDocument(tx.QueryRow(ctx, `
		INSERT INTO documents (id, claim_id, file_name, content_type, size_bytes, sha256, object_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+documentColumns,
		id, c.ID, nd.FileName, nd.ContentType, nd.SizeBytes, nd.SHA256[:], quarantineKey(c.ID, id)))
	if err != nil {
		return Document{}, false, fmt.Errorf("inserting the document: %w", err)
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		ClaimID: c.ID,
		At:      s.now(),
		Actor:   audit.Actor{Type: audit.ActorUser, ID: nd.Principal},
		Action:  audit.ActionDocumentRegistered,
		Details: audit.Details{
			"documentId":  d.ID.String(),
			"contentType": d.ContentType,
			"sizeBytes":   d.SizeBytes,
			"sha256":      hex.EncodeToString(d.SHA256),
		},
	}); err != nil {
		return Document{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, false, fmt.Errorf("committing the registration: %w", err)
	}
	return d, true, nil
}

// existingRegistration decides a registration whose SHA-256 the claim already holds.
func existingRegistration(existing Document, nd NewDocument) (Document, bool, error) {
	switch existing.Status {
	case DocumentPendingUpload:
		if existing.SizeBytes != nd.SizeBytes || existing.ContentType != nd.ContentType {
			return Document{}, false, ErrDeclaredDifferently
		}
		return existing, false, nil
	case DocumentUploaded:
		return Document{}, false, ErrAlreadyUploaded
	case DocumentRejected:
		// The query leaves rejected files out.
	}
	return Document{}, false, fmt.Errorf("document %s is %s, which the lookup excludes", existing.ID, existing.Status)
}

// quarantineKey is where an upload waits until the worker has checked it. It is made from
// IDs only, never the file name the client sent.
func quarantineKey(claimID, documentID uuid.UUID) string {
	return "quarantine/" + claimID.String() + "/" + documentID.String()
}

// Document returns one of a claim's documents, with the claim, for the submitter who
// created the claim. Anyone else gets ErrNotFound, as for a claim that doesn't exist.
func (s *Store) Document(ctx context.Context, principal string, claimID, documentID uuid.UUID) (Claim, Document, error) {
	c, err := s.Get(ctx, claimID)
	if err != nil {
		return Claim{}, Document{}, err
	}
	if c.CreatedBy != principal {
		return Claim{}, Document{}, ErrNotFound
	}
	d, err := scanDocument(s.db.QueryRow(ctx, `
		SELECT `+documentColumns+` FROM documents WHERE id = $1 AND claim_id = $2`, documentID, claimID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Claim{}, Document{}, ErrDocumentNotFound
	}
	if err != nil {
		return Claim{}, Document{}, fmt.Errorf("reading document %s: %w", documentID, err)
	}
	return c, d, nil
}

// RecordUpload records what completing an upload found in the object store: uploaded when
// reason is nil, with the documentUploaded signal for the workflow, or rejected for
// reason. Each comes with its audit event, in one transaction.
//
// Under the claim's lock it checks again that the claim takes documents, since the object
// store was read outside any transaction. A file that is no longer pending_upload is left
// as it is and returned, so a second complete gets the first one's result.
func (s *Store) RecordUpload(ctx context.Context, principal string, claimID, documentID uuid.UUID, reason *RejectionReason) (Document, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Document{}, fmt.Errorf("beginning the upload's record: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	c, err := lockOwned(ctx, tx, claimID, principal)
	if err != nil {
		return Document{}, err
	}
	if !c.AcceptsDocuments() {
		return Document{}, ErrNotAcceptingDocuments
	}
	d, err := scanDocument(tx.QueryRow(ctx, `
		SELECT `+documentColumns+` FROM documents WHERE id = $1 AND claim_id = $2`, documentID, claimID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Document{}, ErrDocumentNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("reading document %s: %w", documentID, err)
	}
	if d.Status != DocumentPendingUpload {
		return d, nil
	}

	at := s.now().UTC().Truncate(time.Microsecond)
	event := audit.Entry{
		ClaimID: c.ID,
		At:      at,
		Actor:   audit.Actor{Type: audit.ActorUser, ID: principal},
		Action:  audit.ActionDocumentUploaded,
		Details: audit.Details{"documentId": d.ID.String()},
	}
	if reason == nil {
		d, err = scanDocument(tx.QueryRow(ctx, `
			UPDATE documents SET status = 'uploaded', uploaded_at = $2
			WHERE id = $1
			RETURNING `+documentColumns, d.ID, at))
		if err != nil {
			return Document{}, fmt.Errorf("marking document %s uploaded: %w", documentID, err)
		}
		payload := struct {
			DocumentID uuid.UUID `json:"documentId"`
		}{d.ID}
		if err := queue(ctx, tx, c, "workflow_signal", SignalDocumentUploaded, payload); err != nil {
			return Document{}, fmt.Errorf("queueing the %s signal: %w", SignalDocumentUploaded, err)
		}
	} else {
		d, err = scanDocument(tx.QueryRow(ctx, `
			UPDATE documents SET status = 'rejected', rejection_reason = $2
			WHERE id = $1
			RETURNING `+documentColumns, d.ID, string(*reason)))
		if err != nil {
			return Document{}, fmt.Errorf("rejecting document %s: %w", documentID, err)
		}
		event.Action = audit.ActionDocumentRejected
		event.Details["reason"] = string(*reason)
	}
	if _, err := audit.Append(ctx, tx, event); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Document{}, fmt.Errorf("committing the upload's record: %w", err)
	}
	return d, nil
}

// lock locks the claim's row until tx ends and returns the claim, or ErrNotFound.
//
// Every transaction that writes an audit event or an outbox row for an existing claim, or
// changes its files or status, calls it first, before it reads anything it checks. The
// lock gives the audit chain its next sequence number without a race, keeps the claim's
// outbox rows in commit order for the relay, and stops an upload and a submission from
// both committing. The claim's row is always locked before any of its documents' rows, so
// two such transactions can't deadlock. No object-store I/O happens while it's held.
func lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Claim, error) {
	c, err := scanClaim(tx.QueryRow(ctx, `SELECT `+claimColumns+` FROM claims WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	if err != nil {
		return Claim{}, fmt.Errorf("locking claim %s: %w", id, err)
	}
	return c, nil
}

// lockOwned locks the claim as lock does and returns it, or ErrNotFound when someone other
// than principal created it: a submitter can't tell another's claim from a missing one.
func lockOwned(ctx context.Context, tx pgx.Tx, id uuid.UUID, principal string) (Claim, error) {
	c, err := lock(ctx, tx, id)
	if err != nil {
		return Claim{}, err
	}
	if c.CreatedBy != principal {
		return Claim{}, ErrNotFound
	}
	return c, nil
}
