package claims

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Status is a claim's place in its lifecycle. The database's CHECK list is the authority;
// these mirror it (CLAUDE.md, "Claim statuses").
type Status string

// The claim statuses, in lifecycle order.
const (
	StatusDraft             Status = "draft"
	StatusSubmitted         Status = "submitted"
	StatusValidating        Status = "validating"
	StatusAwaitingDocuments Status = "awaiting_documents"
	StatusExtracting        Status = "extracting"
	StatusAdjudicating      Status = "adjudicating"
	StatusInReview          Status = "in_review"
	StatusDecided           Status = "decided"
	StatusClosed            Status = "closed"
	StatusRejected          Status = "rejected"
	StatusFailed            Status = "failed"
)

const (
	// WorkflowType is the workflow the relay starts for every new claim (week 1, session 5).
	WorkflowType = "ClaimWorkflow"
	// IdempotencyKeyTTL is how long a key replays its first response.
	IdempotencyKeyTTL = 24 * time.Hour
	// idempotencyLockClass namespaces this package's advisory locks: Postgres has one
	// advisory-lock key space per database, shared by every caller.
	idempotencyLockClass int32 = 0x1d3e
)

// Claim is a claim as stored. Dates are calendar dates at midnight UTC.
type Claim struct {
	ID                 uuid.UUID
	MemberID           uuid.UUID
	Status             Status
	AdmittedOn         time.Time
	DischargedOn       time.Time
	HospitalName       string
	ClaimedAmountPaise int64
	WorkflowID         string
	CreatedBy          string
	Version            int32
	CreatedAt          time.Time
	UpdatedAt          time.Time
	SubmittedAt        *time.Time
	DecidedAt          *time.Time
}

// Draft is what a caller supplies to create a claim.
type Draft struct {
	MemberID           uuid.UUID
	AdmittedOn         time.Time
	DischargedOn       time.Time
	HospitalName       string
	ClaimedAmountPaise int64
}

// Response is a response kept under an idempotency key, so a retry gets it again.
type Response struct {
	Status int
	Body   []byte // JSON
}

// CreateRequest is one idempotent create.
type CreateRequest struct {
	// Principal is the caller. Keys are scoped to it.
	Principal      string
	IdempotencyKey string
	// RequestHash is SHA-256 over the operation and the request, so reusing a key for a
	// different request can be told apart from a retry.
	RequestHash [32]byte
	Draft       Draft
	// Render turns the new claim into the response to keep and return. It runs inside the
	// transaction, so an error from it rolls the whole create back.
	Render func(Claim) (Response, error)
}

// CreateResult is the outcome of Create.
type CreateResult struct {
	ClaimID  uuid.UUID
	Response Response
	// Replayed is true when Response is the stored response of an earlier request.
	Replayed bool
}

// Document is one file of a claim.
type Document struct {
	ID              uuid.UUID
	ClaimID         uuid.UUID
	Status          string
	FileName        string
	ContentType     string
	SizeBytes       int64
	SHA256          []byte
	PageCount       *int32
	RejectionReason *string
	CreatedAt       time.Time
	UploadedAt      *time.Time
}

// Errors that callers turn into client responses.
var (
	// ErrIdempotencyKeyInUse: another request with the same key hasn't finished yet.
	ErrIdempotencyKeyInUse = errors.New("a request with this idempotency key is still in progress")
	// ErrIdempotencyKeyReused: the key was already used for a different request.
	ErrIdempotencyKeyReused = errors.New("the idempotency key was used for a different request")
	// ErrMemberNotFound: the draft names a member that doesn't exist.
	ErrMemberNotFound = errors.New("no such member")
	// ErrNoPolicyYear: no policy year of the member's policy contains the admission date.
	ErrNoPolicyYear = errors.New("no policy year covers the admission date")
	// ErrNotFound: no claim has the ID.
	ErrNotFound = errors.New("no such claim")
)

// DB is the part of a pgx pool the store uses. A pgx.Tx satisfies it too.
type DB interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Store reads and writes claims as claims_app.
type Store struct{ db DB }

// NewStore returns a Store on db.
func NewStore(db DB) *Store { return &Store{db: db} }

// Create creates a draft claim, idempotently. In one transaction it writes the claim, the
// outbox row that starts its workflow (ADR-0003), and the idempotency key with the
// response, so a retry with the same key and request gets that response and no second
// claim. Only successful creates are kept under a key; a request that fails can be retried.
func (s *Store) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return CreateResult{}, fmt.Errorf("beginning the create: %w", err)
	}
	// A no-op once the transaction has committed.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// claims_app can't UPDATE idempotency_keys, so a key row can't say "in progress" and
	// be filled in later. A transaction-scoped advisory lock marks the key as taken
	// instead: a duplicate that arrives meanwhile is told so at once rather than waiting,
	// and the lock goes away with the transaction however it ends.
	class, object := lockKey(req.Principal, req.IdempotencyKey)
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1::int4, $2::int4)`, class, object).Scan(&locked); err != nil {
		return CreateResult{}, fmt.Errorf("locking the idempotency key: %w", err)
	}
	if !locked {
		return CreateResult{}, ErrIdempotencyKeyInUse
	}

	if res, found, err := lookupKey(ctx, tx, req); err != nil || found {
		return res, err
	}
	if err := checkCover(ctx, tx, req.Draft); err != nil {
		return CreateResult{}, err
	}
	claim, err := insertClaim(ctx, tx, req)
	if err != nil {
		return CreateResult{}, err
	}
	if err := insertWorkflowStart(ctx, tx, claim); err != nil {
		return CreateResult{}, err
	}
	resp, err := req.Render(claim)
	if err != nil {
		return CreateResult{}, fmt.Errorf("rendering the response: %w", err)
	}
	if err := insertKey(ctx, tx, req, claim.ID, resp); err != nil {
		return CreateResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, fmt.Errorf("committing the create: %w", err)
	}
	return CreateResult{ClaimID: claim.ID, Response: resp}, nil
}

// lockKey maps a caller's key to an advisory lock. Two keys can share a lock only through
// a hash collision, which at worst makes one of them wait for a retry.
func lockKey(principal, key string) (class, object int32) {
	sum := sha256.Sum256([]byte(principal + "\x00" + key))
	for _, b := range sum[:4] {
		object = object<<8 | int32(b)
	}
	return idempotencyLockClass, object
}

// lookupKey returns the stored response when this key already holds the same request,
// ErrIdempotencyKeyReused when it holds a different one, and found=false when the key is
// unused or has expired (an expired key is deleted, so it can be used again).
func lookupKey(ctx context.Context, tx pgx.Tx, req CreateRequest) (res CreateResult, found bool, err error) {
	var (
		hash    []byte
		status  int
		body    []byte
		claimID *uuid.UUID
		expired bool
	)
	err = tx.QueryRow(ctx, `
		SELECT request_hash, response_status, response_body, claim_id, expires_at <= now()
		FROM idempotency_keys
		WHERE principal = $1 AND idempotency_key = $2`,
		req.Principal, req.IdempotencyKey).Scan(&hash, &status, &body, &claimID, &expired)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return CreateResult{}, false, nil
	case err != nil:
		return CreateResult{}, false, fmt.Errorf("reading the idempotency key: %w", err)
	}
	if expired {
		if _, err := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE principal = $1 AND idempotency_key = $2`,
			req.Principal, req.IdempotencyKey); err != nil {
			return CreateResult{}, false, fmt.Errorf("deleting an expired idempotency key: %w", err)
		}
		return CreateResult{}, false, nil
	}
	if !bytes.Equal(hash, req.RequestHash[:]) {
		return CreateResult{}, false, ErrIdempotencyKeyReused
	}
	res = CreateResult{Response: Response{Status: status, Body: body}, Replayed: true}
	if claimID != nil {
		res.ClaimID = *claimID
	}
	return res, true, nil
}

// checkCover makes sure the member exists and a policy year covers the admission date.
func checkCover(ctx context.Context, tx pgx.Tx, d Draft) error {
	var memberExists, covered bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM members WHERE id = $1),
		       EXISTS (SELECT 1
		               FROM members m
		               JOIN policy_periods p ON p.policy_id = m.policy_id
		               WHERE m.id = $1 AND $2::date BETWEEN p.starts_on AND p.ends_on)`,
		d.MemberID, d.AdmittedOn).Scan(&memberExists, &covered); err != nil {
		return fmt.Errorf("checking the member's cover: %w", err)
	}
	switch {
	case !memberExists:
		return ErrMemberNotFound
	case !covered:
		return ErrNoPolicyYear
	}
	return nil
}

func insertClaim(ctx context.Context, tx pgx.Tx, req CreateRequest) (Claim, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return Claim{}, fmt.Errorf("generating a claim ID: %w", err)
	}
	d := req.Draft
	c, err := scanClaim(tx.QueryRow(ctx, `
		INSERT INTO claims (id, member_id, admitted_on, discharged_on, hospital_name,
		                    claimed_amount_paise, workflow_id, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, member_id, status, admitted_on, discharged_on, hospital_name,
		          claimed_amount_paise, workflow_id, created_by, version, created_at, updated_at,
		          submitted_at, decided_at`,
		id, d.MemberID, d.AdmittedOn, d.DischargedOn, d.HospitalName, d.ClaimedAmountPaise,
		"claim-"+id.String(), req.Principal))
	if err != nil {
		return Claim{}, fmt.Errorf("inserting the claim: %w", err)
	}
	return c, nil
}

// insertWorkflowStart queues the message that starts the claim's workflow. The payload
// carries IDs only (ADR-0004).
func insertWorkflowStart(ctx context.Context, tx pgx.Tx, c Claim) error {
	eventID, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("generating an event ID: %w", err)
	}
	payload, err := json.Marshal(struct {
		ClaimID uuid.UUID `json:"claimId"`
	}{c.ID})
	if err != nil {
		return fmt.Errorf("encoding the workflow payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO outbox (event_id, claim_id, kind, workflow_id, name, payload)
		VALUES ($1, $2, 'workflow_start', $3, $4, $5)`,
		eventID, c.ID, c.WorkflowID, WorkflowType, payload); err != nil {
		return fmt.Errorf("queueing the workflow start: %w", err)
	}
	return nil
}

func insertKey(ctx context.Context, tx pgx.Tx, req CreateRequest, claimID uuid.UUID, resp Response) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (principal, idempotency_key, request_hash, response_status,
		                              response_body, claim_id, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, now() + $7::interval)`,
		req.Principal, req.IdempotencyKey, req.RequestHash[:], resp.Status, resp.Body, claimID, IdempotencyKeyTTL)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		// The lock should make this impossible; if it happens, the client's retry is safe.
		return ErrIdempotencyKeyInUse
	}
	if err != nil {
		return fmt.Errorf("storing the idempotency key: %w", err)
	}
	return nil
}

// Get returns the claim with the ID, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Claim, error) {
	c, err := scanClaim(s.db.QueryRow(ctx, `
		SELECT id, member_id, status, admitted_on, discharged_on, hospital_name,
		       claimed_amount_paise, workflow_id, created_by, version, created_at, updated_at,
		       submitted_at, decided_at
		FROM claims
		WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Claim{}, ErrNotFound
	}
	if err != nil {
		return Claim{}, fmt.Errorf("reading claim %s: %w", id, err)
	}
	return c, nil
}

func scanClaim(row pgx.Row) (Claim, error) {
	var c Claim
	err := row.Scan(&c.ID, &c.MemberID, &c.Status, &c.AdmittedOn, &c.DischargedOn, &c.HospitalName,
		&c.ClaimedAmountPaise, &c.WorkflowID, &c.CreatedBy, &c.Version, &c.CreatedAt, &c.UpdatedAt,
		&c.SubmittedAt, &c.DecidedAt)
	return c, err
}

// Documents returns the claim's documents, oldest first.
func (s *Store) Documents(ctx context.Context, claimID uuid.UUID) ([]Document, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, claim_id, status, file_name, content_type, size_bytes, sha256, page_count,
		       rejection_reason, created_at, uploaded_at
		FROM documents
		WHERE claim_id = $1
		ORDER BY created_at, id`, claimID)
	if err != nil {
		return nil, fmt.Errorf("listing claim %s's documents: %w", claimID, err)
	}
	defer rows.Close()
	docs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Document, error) {
		var d Document
		err := row.Scan(&d.ID, &d.ClaimID, &d.Status, &d.FileName, &d.ContentType, &d.SizeBytes,
			&d.SHA256, &d.PageCount, &d.RejectionReason, &d.CreatedAt, &d.UploadedAt)
		return d, err
	})
	if err != nil {
		return nil, fmt.Errorf("reading claim %s's documents: %w", claimID, err)
	}
	return docs, nil
}
