package workflow_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

// acknowledged records the claims the AcknowledgeClaim activity was run for.
type acknowledged struct {
	mu     sync.Mutex
	claims []string
}

func (a *acknowledged) activity(_ context.Context, claimID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.claims = append(a.claims, claimID)
	return nil
}

func (a *acknowledged) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.claims...)
}

func newEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, *acknowledged) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	// The deadlock detector measures wall time, and its default of 1s fails the workflow
	// (TMPRL1101) whenever a busy machine leaves its goroutine unscheduled that long, as
	// it can under -race or alongside other test binaries. 10s still catches a real
	// deadlock.
	env.SetWorkerOptions(worker.Options{DeadlockDetectionTimeout: 10 * time.Second})
	ack := &acknowledged{}
	env.RegisterActivityWithOptions(ack.activity, activity.RegisterOptions{Name: "AcknowledgeClaim"})
	return env, ack
}

func start() workflow.ClaimInput {
	return workflow.ClaimInput{ClaimID: "01931111-2222-7333-8444-555566667777", EventID: "01931111-2222-7333-8444-000000000001"}
}

// The test environment skips time while a workflow waits: left alone, a workflow waiting
// for a signal runs into the environment's own timeout. So the tests look at the
// workflow's state from a delayed callback, an hour of workflow time in.

func TestClaimWorkflowAcknowledgesTheClaimThenWaitsForItsSubmission(t *testing.T) {
	env, ack := newEnv(t)
	var waiting bool
	var acked []string
	env.RegisterDelayedCallback(func() {
		waiting = !env.IsWorkflowCompleted()
		acked = ack.seen()
	}, time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if !waiting {
		t.Fatalf("an hour in, with no submission, the workflow had finished (error %v)", env.GetWorkflowError())
	}
	if len(acked) != 1 || acked[0] != "01931111-2222-7333-8444-555566667777" {
		t.Fatalf("AcknowledgeClaim ran for %v, want the one claim once", acked)
	}
}

func TestClaimWorkflowCompletesOnceTheClaimIsSubmitted(t *testing.T) {
	env, ack := newEnv(t)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalSubmitted, workflow.Event{EventID: "01931111-2222-7333-8444-000000000002"})
	}, time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if !env.IsWorkflowCompleted() {
		t.Fatal("the workflow is still waiting after the submitted signal")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
	if got := ack.seen(); len(got) != 1 {
		t.Fatalf("AcknowledgeClaim ran %d times, want once", len(got))
	}
}

func TestClaimWorkflowIgnoresOtherSignals(t *testing.T) {
	env, _ := newEnv(t)
	var waiting bool
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("documents_added", workflow.Event{EventID: "01931111-2222-7333-8444-000000000003"})
	}, time.Hour)
	env.RegisterDelayedCallback(func() { waiting = !env.IsWorkflowCompleted() }, 2*time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if !waiting {
		t.Fatalf("a signal other than submitted finished the workflow (error %v)", env.GetWorkflowError())
	}
}
