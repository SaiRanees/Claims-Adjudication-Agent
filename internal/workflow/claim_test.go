package workflow_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

const (
	claimID = "01931111-2222-7333-8444-555566667777"
	docA    = "01931111-2222-7333-8444-0000000000a1"
	docB    = "01931111-2222-7333-8444-0000000000b1"
)

// checker is a fake documents.Checker: it records each call, takes delay to answer, and
// fails with err when it's set.
type checker struct {
	mu       sync.Mutex
	checks   []string
	rejects  []string
	finished int
	delay    time.Duration
	err      error
}

func (c *checker) Check(_ context.Context, _, documentID uuid.UUID) (claims.Document, error) {
	c.mu.Lock()
	c.checks = append(c.checks, documentID.String())
	delay, err := c.delay, c.err
	c.mu.Unlock()
	time.Sleep(delay)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.finished++
	if err != nil {
		return claims.Document{}, err
	}
	return claims.Document{ID: documentID, Status: claims.DocumentAccepted}, nil
}

func (c *checker) Reject(_ context.Context, _, documentID uuid.UUID, reason claims.RejectionReason) (claims.Document, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rejects = append(c.rejects, documentID.String()+" "+string(reason))
	return claims.Document{ID: documentID, Status: claims.DocumentRejected, RejectionReason: &reason}, nil
}

func (c *checker) seen() (checks, rejects []string, finished int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.checks...), append([]string(nil), c.rejects...), c.finished
}

func newEnv(t *testing.T, c *checker) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	// The deadlock detector measures wall time, and its default of 1s fails the workflow
	// (TMPRL1101) whenever a busy machine leaves its goroutine unscheduled that long, as
	// it can under -race or alongside other test binaries. 10s still catches a real
	// deadlock.
	env.SetWorkerOptions(worker.Options{DeadlockDetectionTimeout: 10 * time.Second})
	env.RegisterActivity(&workflow.Activities{Checker: c})
	return env
}

func start() workflow.ClaimInput {
	return workflow.ClaimInput{ClaimID: claimID, EventID: "01931111-2222-7333-8444-000000000001"}
}

func uploaded(eventID, documentID string) workflow.DocumentUploaded {
	return workflow.DocumentUploaded{EventID: eventID, DocumentID: documentID}
}

// The test environment skips time while a workflow waits: left alone, a workflow waiting
// for a signal runs into the environment's own timeout. So the tests act and look from
// delayed callbacks, hours of workflow time in.

func TestClaimWorkflowWaitsForItsSubmission(t *testing.T) {
	env := newEnv(t, &checker{})
	var waiting bool
	env.RegisterDelayedCallback(func() { waiting = !env.IsWorkflowCompleted() }, time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if !waiting {
		t.Fatalf("an hour in, with no submission, the workflow had finished (error %v)", env.GetWorkflowError())
	}
}

func TestClaimWorkflowCompletesOnceTheClaimIsSubmitted(t *testing.T) {
	env := newEnv(t, &checker{})
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalSubmitted, workflow.Event{EventID: "e-submitted"})
	}, time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if !env.IsWorkflowCompleted() {
		t.Fatal("the workflow is still waiting after the submitted signal")
	}
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow error: %v", err)
	}
}

// Each file is checked once, however often its signal arrives, and the workflow doesn't
// end while a check it started is running, even when the submission comes first.
func TestClaimWorkflowChecksEachUploadOnceAndWaitsForTheChecks(t *testing.T) {
	c := &checker{delay: 300 * time.Millisecond}
	env := newEnv(t, c)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalDocumentUploaded, uploaded("e-a", docA))
		env.SignalWorkflow(workflow.SignalDocumentUploaded, uploaded("e-a", docA)) // redelivered
	}, time.Hour)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalDocumentUploaded, uploaded("e-b", docB))
		env.SignalWorkflow(workflow.SignalSubmitted, workflow.Event{EventID: "e-submitted"})
	}, 2*time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if err := env.GetWorkflowError(); err != nil || !env.IsWorkflowCompleted() {
		t.Fatalf("completed %v, error %v", env.IsWorkflowCompleted(), err)
	}
	checks, rejects, finished := c.seen()
	if len(checks) != 2 || checks[0] != docA || checks[1] != docB {
		t.Errorf("checked %v, want %s then %s once each", checks, docA, docB)
	}
	if finished != 2 {
		t.Errorf("the workflow ended with %d of 2 checks finished", finished)
	}
	if len(rejects) != 0 {
		t.Errorf("rejected %v", rejects)
	}
}

// A check that keeps failing on infrastructure retries for about an hour, then the file is
// rejected as check_failed and the claim can still be submitted.
func TestClaimWorkflowRejectsAFileWhoseCheckRunsOutOfRetries(t *testing.T) {
	c := &checker{err: errors.New("object store unreachable")}
	env := newEnv(t, c)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalDocumentUploaded, uploaded("e-a", docA))
	}, time.Hour)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalSubmitted, workflow.Event{EventID: "e-submitted"})
	}, 4*time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if err := env.GetWorkflowError(); err != nil || !env.IsWorkflowCompleted() {
		t.Fatalf("completed %v, error %v", env.IsWorkflowCompleted(), err)
	}
	checks, rejects, _ := c.seen()
	if len(checks) < 5 {
		t.Errorf("the check ran %d times, want it retried", len(checks))
	}
	if len(rejects) != 1 || rejects[0] != docA+" check_failed" {
		t.Errorf("rejected %v, want %s as check_failed", rejects, docA)
	}
}

// A file the store says isn't uploaded isn't retried: retrying can't change that.
func TestCheckDocumentDoesNotRetryAFileThatIsNotUploaded(t *testing.T) {
	c := &checker{err: claims.ErrNotUploaded}
	env := newEnv(t, c)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(workflow.SignalDocumentUploaded, uploaded("e-a", docA))
		env.SignalWorkflow(workflow.SignalSubmitted, workflow.Event{EventID: "e-submitted"})
	}, time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if err := env.GetWorkflowError(); err != nil || !env.IsWorkflowCompleted() {
		t.Fatalf("completed %v, error %v", env.IsWorkflowCompleted(), err)
	}
	if checks, _, _ := c.seen(); len(checks) != 1 {
		t.Errorf("the check ran %d times, want once", len(checks))
	}
}

func TestClaimWorkflowIgnoresOtherSignals(t *testing.T) {
	env := newEnv(t, &checker{})
	var waiting bool
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow("documents_added", workflow.Event{EventID: "e-other"})
	}, time.Hour)
	env.RegisterDelayedCallback(func() { waiting = !env.IsWorkflowCompleted() }, 2*time.Hour)
	env.ExecuteWorkflow(workflow.ClaimWorkflow, start())

	if !waiting {
		t.Fatalf("a signal other than submitted finished the workflow (error %v)", env.GetWorkflowError())
	}
}
