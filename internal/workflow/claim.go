package workflow

import (
	"context"
	"time"

	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"
)

const (
	// TaskQueue is the queue the claim workflow and its activities run on.
	TaskQueue = "claims"
	// ClaimWorkflowName is the workflow type the relay starts for every new claim. It
	// must equal claims.WorkflowType, which the API writes on the outbox row.
	ClaimWorkflowName = "ClaimWorkflow"
	// SignalSubmitted is the signal sent when a claim is submitted.
	SignalSubmitted = "submitted"

	activityAcknowledgeClaim = "AcknowledgeClaim"
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

// ClaimWorkflow is a claim's workflow, with ID claim-<claim id>. For now it is a
// placeholder for the lifecycle in docs/plan/architecture.md: it runs one no-op activity,
// then waits for the claim to be submitted. Later sessions continue from the submission
// with validation, extraction and adjudication; until then the workflow ends there.
//
// Workflow code is deterministic (CLAUDE.md, rule 5): no clock, randomness, I/O or
// goroutines outside the SDK.
func ClaimWorkflow(ctx sdkworkflow.Context, in ClaimInput) error {
	ctx = sdkworkflow.WithActivityOptions(ctx, sdkworkflow.ActivityOptions{StartToCloseTimeout: 30 * time.Second})
	if err := sdkworkflow.ExecuteActivity(ctx, activityAcknowledgeClaim, in.ClaimID).Get(ctx, nil); err != nil {
		return err
	}

	// Only the first submission matters, so a redelivered submitted signal finds the
	// workflow already past this point (ADR-0003: receivers are idempotent).
	var submitted Event
	sdkworkflow.GetSignalChannel(ctx, SignalSubmitted).Receive(ctx, &submitted)
	sdkworkflow.GetLogger(ctx).Info("claim submitted", "claimId", in.ClaimID, "eventId", submitted.EventID)
	return nil
}

// Activities are the claim workflow's activities. Each one is idempotent.
type Activities struct{}

// AcknowledgeClaim is the placeholder's one activity, standing in for the steps to come.
// It does nothing yet.
func (a *Activities) AcknowledgeClaim(_ context.Context, _ string) error {
	return nil
}

// Register registers the claim workflow and its activities on a worker.
func Register(r worker.Registry, activities *Activities) {
	r.RegisterWorkflowWithOptions(ClaimWorkflow, sdkworkflow.RegisterOptions{Name: ClaimWorkflowName})
	r.RegisterActivity(activities)
}
