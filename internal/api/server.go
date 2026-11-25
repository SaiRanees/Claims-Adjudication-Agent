package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/documents"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// Claims is what the handlers need from the claims store.
type Claims interface {
	Create(ctx context.Context, req claims.CreateRequest) (claims.CreateResult, error)
	Get(ctx context.Context, id uuid.UUID) (claims.Claim, error)
	Documents(ctx context.Context, claimID uuid.UUID) ([]claims.Document, error)
	Submit(ctx context.Context, principal string, claimID uuid.UUID) (claims.Claim, error)
}

// Documents is what the handlers need for uploads: documents.Service.
type Documents interface {
	Register(ctx context.Context, nd claims.NewDocument) (documents.Registered, error)
	Complete(ctx context.Context, principal string, claimID, documentID uuid.UUID) (claims.Document, error)
}

// ErrNotImplemented is what every operation not built yet returns. The strict handler's
// response-error hook turns it into a 501 problem: oapi-codegen's net/http output has no
// ready-made "unimplemented" type.
var ErrNotImplemented = errors.New("not implemented yet")

// Server implements the contract in api/openapi.yaml.
type Server struct {
	claims    Claims
	documents Documents
	// limiter caps each caller's rate of registering files and submitting claims.
	limiter  *RateLimiter
	log      *slog.Logger
	draining atomic.Bool
}

var _ apigen.StrictServerInterface = (*Server)(nil)

// NewServer returns a Server that keeps claims in c and runs uploads through d, with each
// caller's registrations and submissions limited by limiter.
func NewServer(c Claims, d Documents, limiter *RateLimiter, log *slog.Logger) *Server {
	return &Server{claims: c, documents: d, limiter: limiter, log: log}
}

// Drain makes /healthz answer 503, so a load balancer stops sending new traffic while
// the process shuts down.
func (s *Server) Drain() { s.draining.Store(true) }

// GetHealth reports whether the process is taking traffic.
func (s *Server) GetHealth(ctx context.Context, _ apigen.GetHealthRequestObject) (apigen.GetHealthResponseObject, error) {
	if s.draining.Load() {
		return apigen.GetHealth503ApplicationProblemPlusJSONResponse{
			ServiceUnavailableApplicationProblemPlusJSONResponse: apigen.ServiceUnavailableApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusServiceUnavailable, typeUnavailable, "shutting_down", "The process is shutting down.")),
		}, nil
	}
	return apigen.GetHealth200JSONResponse{Status: apigen.Ok}, nil
}

// CreateClaim creates a draft claim, idempotently. Role: submitter.
func (s *Server) CreateClaim(ctx context.Context, req apigen.CreateClaimRequestObject) (apigen.CreateClaimResponseObject, error) {
	caller, ok := principalFrom(ctx)
	if !ok {
		return nil, errors.New("no caller on an authenticated operation")
	}
	if caller.Role != platform.RoleSubmitter {
		return apigen.CreateClaim403ApplicationProblemPlusJSONResponse{
			ForbiddenApplicationProblemPlusJSONResponse: apigen.ForbiddenApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusForbidden, typeForbidden, "role_not_allowed", "Only submitters can create claims.")),
		}, nil
	}
	body := req.Body
	if body.DischargedOn.Before(body.AdmittedOn.Time) {
		return apigen.CreateClaim400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: apigen.BadRequestApplicationProblemPlusJSONResponse(
				validationFailed(ctx, apigen.FieldError{Pointer: "/dischargedOn", Detail: "must be on or after admittedOn"})),
		}, nil
	}
	hash, err := requestHash("createClaim", body)
	if err != nil {
		return nil, err
	}

	res, err := s.claims.Create(ctx, claims.CreateRequest{
		Principal:      caller.Subject,
		IdempotencyKey: req.Params.IdempotencyKey,
		RequestHash:    hash,
		Draft: claims.Draft{
			MemberID:           body.MemberID,
			AdmittedOn:         body.AdmittedOn.Time,
			DischargedOn:       body.DischargedOn.Time,
			HospitalName:       body.HospitalName,
			ClaimedAmountPaise: body.ClaimedAmountPaise,
		},
		Render: renderCreated,
	})
	switch {
	case errors.Is(err, claims.ErrIdempotencyKeyInUse):
		return apigen.CreateClaim409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: apigen.ConflictApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusConflict, typeConflict, "idempotency_key_in_use",
					"A request with this Idempotency-Key is still in progress. Retry it shortly.")),
		}, nil
	case errors.Is(err, claims.ErrIdempotencyKeyReused):
		return unprocessable(problem(ctx, http.StatusUnprocessableEntity, typeUnprocessable, "idempotency_key_reused",
			"This Idempotency-Key was already used for a different request. Use a new key.")), nil
	case errors.Is(err, claims.ErrMemberNotFound):
		return unprocessable(withErrors(problem(ctx, http.StatusUnprocessableEntity, typeUnprocessable, "member_not_found",
			"No member has this ID."), apigen.FieldError{Pointer: "/memberId", Detail: "no such member"})), nil
	case errors.Is(err, claims.ErrNoPolicyYear):
		return unprocessable(withErrors(problem(ctx, http.StatusUnprocessableEntity, typeUnprocessable, "no_policy_year",
			"No policy year of the member's policy covers the admission date."),
			apigen.FieldError{Pointer: "/admittedOn", Detail: "outside every policy year"})), nil
	case err != nil:
		return nil, fmt.Errorf("creating a claim: %w", err)
	}

	// The body is the one stored under the key, so a retry gets exactly the first response.
	if res.Response.Status != http.StatusCreated {
		return nil, fmt.Errorf("the idempotency key holds an unexpected status %d", res.Response.Status)
	}
	var view apigen.Claim
	if err := json.Unmarshal(res.Response.Body, &view); err != nil {
		return nil, fmt.Errorf("decoding the stored response: %w", err)
	}
	location := "/v1/claims/" + res.ClaimID.String()
	return apigen.CreateClaim201JSONResponse{
		Body:    view,
		Headers: apigen.CreateClaim201ResponseHeaders{Location: &location},
	}, nil
}

func unprocessable(p apigen.Problem) apigen.CreateClaim422ApplicationProblemPlusJSONResponse {
	return apigen.CreateClaim422ApplicationProblemPlusJSONResponse{
		UnprocessableContentApplicationProblemPlusJSONResponse: apigen.UnprocessableContentApplicationProblemPlusJSONResponse(p),
	}
}

// GetClaim returns a claim. Roles: the submitter who created it, reviewer, admin.
func (s *Server) GetClaim(ctx context.Context, req apigen.GetClaimRequestObject) (apigen.GetClaimResponseObject, error) {
	caller, ok := principalFrom(ctx)
	if !ok {
		return nil, errors.New("no caller on an authenticated operation")
	}
	notFound := apigen.GetClaim404ApplicationProblemPlusJSONResponse{
		NotFoundApplicationProblemPlusJSONResponse: apigen.NotFoundApplicationProblemPlusJSONResponse(notFoundProblem(ctx)),
	}
	c, err := s.claims.Get(ctx, req.ClaimID)
	if errors.Is(err, claims.ErrNotFound) {
		return notFound, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading claim %s: %w", req.ClaimID, err)
	}
	// A submitter sees only their own claims, and can't tell someone else's from a missing one.
	if caller.Role == platform.RoleSubmitter && c.CreatedBy != caller.Subject {
		return notFound, nil
	}
	docs, err := s.claims.Documents(ctx, c.ID)
	if err != nil {
		return nil, fmt.Errorf("listing claim %s's documents: %w", c.ID, err)
	}
	return apigen.GetClaim200JSONResponse(claimView(c, docs)), nil
}

// CreateDocumentUpload registers a file and returns a signed URL to upload it to: 201 for a
// new file, 200 for one already registered and still waiting for its upload. Role: the
// submitter who created the claim, within the per-caller rate limit.
func (s *Server) CreateDocumentUpload(ctx context.Context, req apigen.CreateDocumentUploadRequestObject) (apigen.CreateDocumentUploadResponseObject, error) {
	caller, ok := principalFrom(ctx)
	if !ok {
		return nil, errors.New("no caller on an authenticated operation")
	}
	if caller.Role != platform.RoleSubmitter {
		return apigen.CreateDocumentUpload403ApplicationProblemPlusJSONResponse{
			ForbiddenApplicationProblemPlusJSONResponse: apigen.ForbiddenApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusForbidden, typeForbidden, "role_not_allowed", "Only submitters can upload documents.")),
		}, nil
	}
	if limited, ok := s.rateLimited(ctx, caller.Subject); !ok {
		return apigen.CreateDocumentUpload429ApplicationProblemPlusJSONResponse{TooManyRequestsApplicationProblemPlusJSONResponse: limited}, nil
	}
	body := req.Body
	var sum [32]byte
	// The contract's pattern holds the hash to 64 lower-case hex digits, so a failure here
	// is ours, not the client's.
	if _, err := hex.Decode(sum[:], []byte(body.Sha256)); err != nil {
		return nil, fmt.Errorf("decoding a SHA-256 the contract let through: %w", err)
	}

	res, err := s.documents.Register(ctx, claims.NewDocument{
		Principal:   caller.Subject,
		ClaimID:     req.ClaimID,
		FileName:    body.FileName,
		ContentType: string(body.ContentType),
		SizeBytes:   body.SizeBytes,
		SHA256:      sum,
	})
	conflict := func(code, detail string) apigen.CreateDocumentUpload409ApplicationProblemPlusJSONResponse {
		return apigen.CreateDocumentUpload409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: apigen.ConflictApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusConflict, typeConflict, code, detail)),
		}
	}
	switch {
	case errors.Is(err, claims.ErrNotFound):
		return apigen.CreateDocumentUpload404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: apigen.NotFoundApplicationProblemPlusJSONResponse(notFoundProblem(ctx)),
		}, nil
	case errors.Is(err, claims.ErrNotAcceptingDocuments):
		return conflict(codeNotAcceptingDocuments, detailNotAcceptingDocuments), nil
	case errors.Is(err, claims.ErrAlreadyUploaded):
		return conflict("document_already_uploaded", "This file (same SHA-256) is already uploaded to the claim."), nil
	case errors.Is(err, claims.ErrDeclaredDifferently):
		return conflict("document_declared_differently",
			"This file (same SHA-256) is already registered on the claim with a different size or content type."), nil
	case errors.Is(err, claims.ErrTooManyFiles):
		return conflict("too_many_documents",
			fmt.Sprintf("A claim can have at most %d files, rejected ones included.", claims.MaxFilesPerClaim)), nil
	case err != nil:
		return nil, fmt.Errorf("registering a document on claim %s: %w", req.ClaimID, err)
	}
	if res.Created {
		return apigen.CreateDocumentUpload201JSONResponse(uploadView(res)), nil
	}
	return apigen.CreateDocumentUpload200JSONResponse(uploadView(res)), nil
}

// The conflict a claim past accepting documents gets, from either upload operation.
const (
	codeNotAcceptingDocuments   = "claim_not_accepting_documents"
	detailNotAcceptingDocuments = "The claim is past accepting documents: it's no longer a draft or waiting for documents."
)

// CompleteDocumentUpload checks an uploaded file against its declaration and returns it,
// now uploaded or rejected. Role: the submitter who created the claim.
func (s *Server) CompleteDocumentUpload(ctx context.Context, req apigen.CompleteDocumentUploadRequestObject) (apigen.CompleteDocumentUploadResponseObject, error) {
	caller, ok := principalFrom(ctx)
	if !ok {
		return nil, errors.New("no caller on an authenticated operation")
	}
	if caller.Role != platform.RoleSubmitter {
		return apigen.CompleteDocumentUpload403ApplicationProblemPlusJSONResponse{
			ForbiddenApplicationProblemPlusJSONResponse: apigen.ForbiddenApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusForbidden, typeForbidden, "role_not_allowed", "Only submitters can upload documents.")),
		}, nil
	}
	d, err := s.documents.Complete(ctx, caller.Subject, req.ClaimID, req.DocumentID)
	switch {
	case errors.Is(err, claims.ErrNotFound), errors.Is(err, claims.ErrDocumentNotFound):
		return apigen.CompleteDocumentUpload404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: apigen.NotFoundApplicationProblemPlusJSONResponse(notFoundProblem(ctx)),
		}, nil
	case errors.Is(err, claims.ErrNotAcceptingDocuments):
		return apigen.CompleteDocumentUpload409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: apigen.ConflictApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusConflict, typeConflict, codeNotAcceptingDocuments, detailNotAcceptingDocuments)),
		}, nil
	case err != nil:
		return nil, fmt.Errorf("completing document %s: %w", req.DocumentID, err)
	}
	return apigen.CompleteDocumentUpload200JSONResponse(documentView(d)), nil
}

// The operations below arrive in later sessions; until then they answer 501.

// GetAdjudication is not built yet.
func (s *Server) GetAdjudication(context.Context, apigen.GetAdjudicationRequestObject) (apigen.GetAdjudicationResponseObject, error) {
	return nil, ErrNotImplemented
}

// ListAuditEvents is not built yet.
func (s *Server) ListAuditEvents(context.Context, apigen.ListAuditEventsRequestObject) (apigen.ListAuditEventsResponseObject, error) {
	return nil, ErrNotImplemented
}

// StreamClaimEvents is not built yet.
func (s *Server) StreamClaimEvents(context.Context, apigen.StreamClaimEventsRequestObject) (apigen.StreamClaimEventsResponseObject, error) {
	return nil, ErrNotImplemented
}

// SubmitClaim submits a draft claim once its files are checked, and returns it; a claim
// already submitted comes back as it is. Role: the submitter who created the claim, within
// the per-caller rate limit it shares with registering files.
func (s *Server) SubmitClaim(ctx context.Context, req apigen.SubmitClaimRequestObject) (apigen.SubmitClaimResponseObject, error) {
	caller, ok := principalFrom(ctx)
	if !ok {
		return nil, errors.New("no caller on an authenticated operation")
	}
	if caller.Role != platform.RoleSubmitter {
		return apigen.SubmitClaim403ApplicationProblemPlusJSONResponse{
			ForbiddenApplicationProblemPlusJSONResponse: apigen.ForbiddenApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusForbidden, typeForbidden, "role_not_allowed", "Only submitters can submit claims.")),
		}, nil
	}
	if limited, ok := s.rateLimited(ctx, caller.Subject); !ok {
		return apigen.SubmitClaim429ApplicationProblemPlusJSONResponse{TooManyRequestsApplicationProblemPlusJSONResponse: limited}, nil
	}
	conflict := func(code, detail string) apigen.SubmitClaim409ApplicationProblemPlusJSONResponse {
		return apigen.SubmitClaim409ApplicationProblemPlusJSONResponse{
			ConflictApplicationProblemPlusJSONResponse: apigen.ConflictApplicationProblemPlusJSONResponse(
				problem(ctx, http.StatusConflict, typeConflict, code, detail)),
		}
	}
	c, err := s.claims.Submit(ctx, caller.Subject, req.ClaimID)
	switch {
	case errors.Is(err, claims.ErrNotFound):
		return apigen.SubmitClaim404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: apigen.NotFoundApplicationProblemPlusJSONResponse(notFoundProblem(ctx)),
		}, nil
	case errors.Is(err, claims.ErrChecksPending):
		return conflict("document_checks_pending",
			"A document is uploaded and still being checked. Submit once every document is accepted or rejected."), nil
	case errors.Is(err, claims.ErrNoAcceptedDocuments):
		return conflict("no_accepted_documents", "The claim has no accepted document to submit."), nil
	case err != nil:
		return nil, fmt.Errorf("submitting claim %s: %w", req.ClaimID, err)
	}
	docs, err := s.claims.Documents(ctx, c.ID)
	if err != nil {
		return nil, fmt.Errorf("listing claim %s's documents: %w", c.ID, err)
	}
	return apigen.SubmitClaim202JSONResponse(claimView(c, docs)), nil
}

// rateLimited takes one of the caller's tokens, or returns the 429 to send.
func (s *Server) rateLimited(ctx context.Context, subject string) (apigen.TooManyRequestsApplicationProblemPlusJSONResponse, bool) {
	allowed, wait := s.limiter.Allow(subject)
	if allowed {
		return apigen.TooManyRequestsApplicationProblemPlusJSONResponse{}, true
	}
	seconds := retryAfter(wait)
	return apigen.TooManyRequestsApplicationProblemPlusJSONResponse{
		Body: problem(ctx, http.StatusTooManyRequests, typeTooManyRequests, "rate_limited",
			"Too many requests. Retry after the number of seconds in Retry-After."),
		Headers: apigen.TooManyRequestsResponseHeaders{RetryAfter: &seconds},
	}, false
}

// ListReviewTasks is not built yet.
func (s *Server) ListReviewTasks(context.Context, apigen.ListReviewTasksRequestObject) (apigen.ListReviewTasksResponseObject, error) {
	return nil, ErrNotImplemented
}

// ClaimReviewTask is not built yet.
func (s *Server) ClaimReviewTask(context.Context, apigen.ClaimReviewTaskRequestObject) (apigen.ClaimReviewTaskResponseObject, error) {
	return nil, ErrNotImplemented
}

// DecideReviewTask is not built yet.
func (s *Server) DecideReviewTask(context.Context, apigen.DecideReviewTaskRequestObject) (apigen.DecideReviewTaskResponseObject, error) {
	return nil, ErrNotImplemented
}
