package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

const (
	// TaskQueue is the queue the claim workflow and its activities run on.
	TaskQueue = "claims"
	// ClaimWorkflowName is the workflow type the relay starts for every new claim. It
	// must equal claims.WorkflowType, which the API writes on the outbox row.
	ClaimWorkflowName = "ClaimWorkflow"
	// SignalSubmitted is the signal sent when a claim is submitted.
	SignalSubmitted = "submitted"
	// SignalDocumentUploaded is sent when a file is uploaded and waits for its check.
	SignalDocumentUploaded = claims.SignalDocumentUploaded

	activityCheckDocument  = "CheckDocument"
	activityRejectDocument = "RejectDocument"
)

// How a check retries. Only infrastructure errors reach Temporal (a bad file is a
// verdict), and they back off to a 5-minute interval for about an hour. Then the file is
// rejected with check_failed (docs/plan/week-3.md, "Decide first").
const (
	checkAttemptTimeout = 2 * time.Minute
	checkRetryBudget    = time.Hour
	checkMaxInterval    = 5 * time.Minute
)

// ClaimInput starts a claim's workflow: the outbox row's payload plus its event ID.
// IDs only (ADR-0004).
type ClaimInput struct {
	ClaimID string `json:"claimId"`
	EventID string `json:"eventId"`
}

// Event is the payload of a signal: the ID of the outbox event that carried it, so a
// workflow can ignore a redelivered one.
type Event struct {
	EventID string `json:"eventId"`
}

// DocumentUploaded is the documentUploaded signal's payload.
type DocumentUploaded struct {
	EventID    string `json:"eventId"`
	DocumentID string `json:"documentId"`
}

// DocumentRef names one file of a claim: an activity's input.
type DocumentRef struct {
	ClaimID    string `json:"claimId"`
	DocumentID string `json:"documentId"`
}

// CheckResult is a check's outcome: the file's status and, if rejected, why. Small enums
// only (ADR-0004).
type CheckResult struct {
	Status claims.DocumentStatus   `json:"status"`
	Reason *claims.RejectionReason `json:"reason,omitempty"`
}

// ClaimWorkflow is a claim's workflow, with ID claim-<claim id>. While the claim is a
// draft it checks each uploaded file as its documentUploaded signal arrives. Once the
// claim is submitted and every check it started has finished, it ends, for now; later
// sessions continue with preparation, extraction and adjudication.
//
// Submit needs no file still uploaded, and complete refuses once the claim is submitted,
// so no upload signal follows the submission.
//
// Workflow code is deterministic (CLAUDE.md, rule 5): no clock, randomness, I/O or
// goroutines outside the SDK.
func ClaimWorkflow(ctx sdkworkflow.Context, in ClaimInput) error {
	log := sdkworkflow.GetLogger(ctx)
	checkCtx := sdkworkflow.WithActivityOptions(ctx, sdkworkflow.ActivityOptions{
		StartToCloseTimeout:    checkAttemptTimeout,
		ScheduleToCloseTimeout: checkRetryBudget,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    checkMaxInterval,
		},
	})
	// Recording check_failed is one database write: it keeps retrying until it lands.
	rejectCtx := sdkworkflow.WithActivityOptions(ctx, sdkworkflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumInterval: checkMaxInterval},
	})

	seen := map[string]bool{}
	running := 0
	submitted := false
	sel := sdkworkflow.NewSelector(ctx)

	sel.AddReceive(sdkworkflow.GetSignalChannel(ctx, SignalDocumentUploaded), func(c sdkworkflow.ReceiveChannel, _ bool) {
		var up DocumentUploaded
		c.Receive(ctx, &up)
		// The relay can deliver a message twice (ADR-0003), and a file is uploaded once.
		if seen[up.EventID] || seen[up.DocumentID] {
			return
		}
		seen[up.EventID], seen[up.DocumentID] = true, true
		ref := DocumentRef{ClaimID: in.ClaimID, DocumentID: up.DocumentID}
		running++
		sel.AddFuture(sdkworkflow.ExecuteActivity(checkCtx, activityCheckDocument, ref), func(f sdkworkflow.Future) {
			var res CheckResult
			err := f.Get(ctx, &res)
			if err == nil {
				running--
				return
			}
			log.Error("a file's check ran out of retries; rejecting it as check_failed",
				"claimId", ref.ClaimID, "documentId", ref.DocumentID, "error", err)
			sel.AddFuture(sdkworkflow.ExecuteActivity(rejectCtx, activityRejectDocument, ref), func(f sdkworkflow.Future) {
				running--
				if err := f.Get(ctx, nil); err != nil {
					log.Error("recording check_failed failed", "claimId", ref.ClaimID, "documentId", ref.DocumentID, "error", err)
				}
			})
		})
	})

	// Only the first submission matters, so a redelivered submitted signal changes nothing
	// (ADR-0003: receivers are idempotent).
	sel.AddReceive(sdkworkflow.GetSignalChannel(ctx, SignalSubmitted), func(c sdkworkflow.ReceiveChannel, _ bool) {
		var ev Event
		c.Receive(ctx, &ev)
		if !submitted {
			submitted = true
			log.Info("claim submitted", "claimId", in.ClaimID, "eventId", ev.EventID)
		}
	})

	for !submitted || running > 0 {
		sel.Select(ctx)
	}
	return nil
}

// Checker checks files; documents.Checker provides it.
type Checker interface {
	Check(ctx context.Context, claimID, documentID uuid.UUID) (claims.Document, error)
	Reject(ctx context.Context, claimID, documentID uuid.UUID, reason claims.RejectionReason) (claims.Document, error)
}

// Activities are the claim workflow's activities. Each one is idempotent.
type Activities struct {
	Checker Checker
}

// CheckDocument checks one uploaded file and commits its verdict (documents.Checker). A
// file already decided returns its stored verdict.
func (a *Activities) CheckDocument(ctx context.Context, ref DocumentRef) (CheckResult, error) {
	claimID, documentID, err := ref.ids()
	if err != nil {
		return CheckResult{}, err
	}
	d, err := a.Checker.Check(ctx, claimID, documentID)
	if err != nil {
		return CheckResult{}, permanent(err)
	}
	return CheckResult{Status: d.Status, Reason: d.RejectionReason}, nil
}

// RejectDocument records check_failed for a file whose check ran out of retries. A file
// decided in the meantime keeps its verdict.
func (a *Activities) RejectDocument(ctx context.Context, ref DocumentRef) error {
	claimID, documentID, err := ref.ids()
	if err != nil {
		return err
	}
	_, err = a.Checker.Reject(ctx, claimID, documentID, claims.RejectCheckFailed)
	return permanent(err)
}

// permanent stops retries for errors a retry can't fix: a file that isn't uploaded or
// doesn't exist.
func permanent(err error) error {
	if errors.Is(err, claims.ErrNotUploaded) || errors.Is(err, claims.ErrDocumentNotFound) {
		return temporal.NewNonRetryableApplicationError(err.Error(), "document", err)
	}
	return err
}

func (r DocumentRef) ids() (uuid.UUID, uuid.UUID, error) {
	claimID, err := uuid.Parse(r.ClaimID)
	if err != nil {
		return uuid.Nil, uuid.Nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("claim ID %q", r.ClaimID), "input", err)
	}
	documentID, err := uuid.Parse(r.DocumentID)
	if err != nil {
		return uuid.Nil, uuid.Nil, temporal.NewNonRetryableApplicationError(fmt.Sprintf("document ID %q", r.DocumentID), "input", err)
	}
	return claimID, documentID, nil
}

// Register registers the claim workflow and its activities on a worker.
func Register(r worker.Registry, activities *Activities) {
	r.RegisterWorkflowWithOptions(ClaimWorkflow, sdkworkflow.RegisterOptions{Name: ClaimWorkflowName})
	r.RegisterActivity(activities)
}
