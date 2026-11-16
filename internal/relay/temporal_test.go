package relay_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/relay"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

// TestTemporalDeliveryRunsTheClaimWorkflow takes one claim from its outbox rows to a
// finished workflow, with the real relay, Temporal (TEST_TEMPORAL_ADDRESS, namespace
// claims) and a worker running the real workflow code.
func TestTemporalDeliveryRunsTheClaimWorkflow(t *testing.T) {
	addr := os.Getenv("TEST_TEMPORAL_ADDRESS")
	if addr == "" {
		t.Skip("TEST_TEMPORAL_ADDRESS is not set; this test needs Temporal with the claims namespace")
	}
	pool := connect(t)
	ctx := context.Background()

	c, err := client.Dial(client.Options{HostPort: addr, Namespace: "claims", Logger: sdklog.NewStructuredLogger(quiet)})
	if err != nil {
		t.Fatalf("connecting to Temporal: %v", err)
	}
	defer c.Close()
	w := worker.New(c, workflow.TaskQueue, worker.Options{})
	workflow.Register(w, &workflow.Activities{})
	if err := w.Start(); err != nil {
		t.Fatalf("starting the worker: %v", err)
	}
	defer w.Stop()

	tx := isolated(t, pool)
	claim := newClaim(t, tx)
	start := startEvent(t, tx, claim)
	id := "claim-" + claim.String()
	temporal := relay.NewTemporal(c, workflow.TaskQueue)
	r := relay.New(tx, temporal, quiet)

	// The claim's start row starts its workflow, with the claim's and the event's IDs.
	if !deliverNext(t, r) || !readRow(t, tx, start).published {
		t.Fatal("the start wasn't delivered")
	}
	if got := status(t, c, id); got != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
		t.Fatalf("workflow %s is %v, want running", id, got)
	}
	if in := startInput(t, c, id); in.ClaimID != claim.String() || in.EventID != start.String() {
		t.Fatalf("the workflow started with %+v, want claim %s and event %s", in, claim, start)
	}

	// Delivery is at least once: the same start again finds the running workflow.
	startMsg := relay.Message{EventID: start, ClaimID: claim, Kind: relay.KindWorkflowStart, WorkflowID: id,
		Name: workflow.ClaimWorkflowName, Payload: fmt.Appendf(nil, `{"claimId":%q}`, claim)}
	if err := temporal.Deliver(ctx, startMsg); err != nil {
		t.Fatalf("delivering the start a second time: %v", err)
	}

	// The submitted signal ends the placeholder workflow.
	submitted := addSignal(t, tx, claim, workflow.SignalSubmitted)
	if !deliverNext(t, r) || !readRow(t, tx, submitted).published {
		t.Fatal("the submitted signal wasn't delivered")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.GetWorkflow(waitCtx, id, "").Get(waitCtx, nil); err != nil {
		t.Fatalf("the workflow didn't finish after the submitted signal: %v", err)
	}

	// A claim has one workflow, ever: a late start doesn't begin a second run.
	if err := temporal.Deliver(ctx, startMsg); err != nil {
		t.Fatalf("delivering the start after the workflow finished: %v", err)
	}
	if got := status(t, c, id); got != enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED {
		t.Fatalf("after a late start the latest run of %s is %v, want the completed one", id, got)
	}

	// A signal for the closed workflow can never be delivered.
	late := relay.Message{EventID: submitted, ClaimID: claim, Kind: relay.KindWorkflowSignal, WorkflowID: id,
		Name: workflow.SignalSubmitted, Payload: fmt.Appendf(nil, `{"claimId":%q}`, claim)}
	if err := temporal.Deliver(ctx, late); !errors.Is(err, relay.ErrUndeliverable) {
		t.Fatalf("signalling the closed workflow: err = %v, want ErrUndeliverable", err)
	}
}

func status(t *testing.T, c client.Client, id string) enumspb.WorkflowExecutionStatus {
	t.Helper()
	desc, err := c.DescribeWorkflowExecution(context.Background(), id, "")
	if err != nil {
		t.Fatalf("describing %s: %v", id, err)
	}
	return desc.GetWorkflowExecutionInfo().GetStatus()
}

// startInput reads the workflow's input from its first history event.
func startInput(t *testing.T, c client.Client, id string) workflow.ClaimInput {
	t.Helper()
	events := c.GetWorkflowHistory(context.Background(), id, "", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	first, err := events.Next()
	if err != nil {
		t.Fatalf("reading %s's history: %v", id, err)
	}
	var in workflow.ClaimInput
	if err := converter.GetDefaultDataConverter().FromPayloads(
		first.GetWorkflowExecutionStartedEventAttributes().GetInput(), &in); err != nil {
		t.Fatalf("decoding %s's input: %v", id, err)
	}
	return in
}
