package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/api/apigen"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

// Claims is what the handlers need from the claims store.
type Claims interface {
	Create(ctx context.Context, req claims.CreateRequest) (claims.CreateResult, error)
	Get(ctx context.Context, id uuid.UUID) (claims.Claim, error)
	Documents(ctx context.Context, claimID uuid.UUID) ([]claims.Document, error)
}

// ErrNotImplemented is what every operation not built yet returns. The strict handler's
// response-error hook turns it into a 501 problem: oapi-codegen's net/http output has no
// ready-made "unimplemented" type.
var ErrNotImplemented = errors.New("not implemented yet")

// Server implements the contract in api/openapi.yaml.
type Server struct {
	claims   Claims
	log      *slog.Logger
	draining atomic.Bool
}

var _ apigen.StrictServerInterface = (*Server)(nil)

// NewServer returns a Server that keeps claims in c.
func NewServer(c Claims, log *slog.Logger) *Server {
	return &Server{claims: c, log: log}
}

// Drain makes /healthz answer 503, so a load balancer stops sending new traffic while
// the process shuts down.
func (s *Server) Drain() { s.draining.Store(true) }

// GetHealth reports whether the process is taking traffic.
func (s *Server) GetHealth(context.Context, apigen.GetHealthRequestObject) (apigen.GetHealthResponseObject, error) {
	if s.draining.Load() {
		return apigen.GetHealth503ApplicationProblemPlusJSONResponse{
			ServiceUnavailableApplicationProblemPlusJSONResponse: apigen.ServiceUnavailableApplicationProblemPlusJSONResponse(
				problem(http.StatusServiceUnavailable, typeUnavailable, "shutting_down", "The process is shutting down.")),
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
				problem(http.StatusForbidden, typeForbidden, "role_not_allowed", "Only submitters can create claims.")),
		}, nil
	}
	body := req.Body
	if body.DischargedOn.Before(body.AdmittedOn.Time) {
		return apigen.CreateClaim400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: apigen.BadRequestApplicationProblemPlusJSONResponse(
				validationFailed(apigen.FieldError{Pointer: "/dischargedOn", Detail: "must be on or after admittedOn"})),
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
				problem(http.StatusConflict, typeConflict, "idempotency_key_in_use",
					"A request with this Idempotency-Key is still in progress. Retry it shortly.")),
		}, nil
	case errors.Is(err, claims.ErrIdempotencyKeyReused):
		return unprocessable(problem(http.StatusUnprocessableEntity, typeUnprocessable, "idempotency_key_reused",
			"This Idempotency-Key was already used for a different request. Use a new key.")), nil
	case errors.Is(err, claims.ErrMemberNotFound):
		return unprocessable(withErrors(problem(http.StatusUnprocessableEntity, typeUnprocessable, "member_not_found",
			"No member has this ID."), apigen.FieldError{Pointer: "/memberId", Detail: "no such member"})), nil
	case errors.Is(err, claims.ErrNoPolicyYear):
		return unprocessable(withErrors(problem(http.StatusUnprocessableEntity, typeUnprocessable, "no_policy_year",
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
		NotFoundApplicationProblemPlusJSONResponse: apigen.NotFoundApplicationProblemPlusJSONResponse(notFoundProblem()),
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

// The operations below arrive in later sessions; until then they answer 501.

// GetAdjudication is not built yet.
func (s *Server) GetAdjudication(context.Context, apigen.GetAdjudicationRequestObject) (apigen.GetAdjudicationResponseObject, error) {
	return nil, ErrNotImplemented
}

// ListAuditEvents is not built yet.
func (s *Server) ListAuditEvents(context.Context, apigen.ListAuditEventsRequestObject) (apigen.ListAuditEventsResponseObject, error) {
	return nil, ErrNotImplemented
}

// CreateDocumentUpload is not built yet.
func (s *Server) CreateDocumentUpload(context.Context, apigen.CreateDocumentUploadRequestObject) (apigen.CreateDocumentUploadResponseObject, error) {
	return nil, ErrNotImplemented
}

// CompleteDocumentUpload is not built yet.
func (s *Server) CompleteDocumentUpload(context.Context, apigen.CompleteDocumentUploadRequestObject) (apigen.CompleteDocumentUploadResponseObject, error) {
	return nil, ErrNotImplemented
}

// StreamClaimEvents is not built yet.
func (s *Server) StreamClaimEvents(context.Context, apigen.StreamClaimEventsRequestObject) (apigen.StreamClaimEventsResponseObject, error) {
	return nil, ErrNotImplemented
}

// SubmitClaim is not built yet.
func (s *Server) SubmitClaim(context.Context, apigen.SubmitClaimRequestObject) (apigen.SubmitClaimResponseObject, error) {
	return nil, ErrNotImplemented
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
