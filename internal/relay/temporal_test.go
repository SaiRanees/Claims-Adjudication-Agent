package relay_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/relay"
	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/workflow"
)

// TestTemporalDeliveryRunsTheClaimWorkflow takes one claim from its outbox rows to a
// finished workflow, with the real relay, Temporal (TEST_TEMPORAL_ADDRESS, namespace
// claims) and a worker running the real workflow code, all traced as the services are:
// the workflow joins the trace of the request that created the claim.
func TestTemporalDeliveryRunsTheClaimWorkflow(t *testing.T) {
	addr := os.Getenv("TEST_TEMPORAL_ADDRESS")
	if addr == "" {
		t.Skip("TEST_TEMPORAL_ADDRESS is not set; this test needs Temporal with the claims namespace")
	}
	pool := connect(t)
	ctx := context.Background()
	spans := recordSpans(t)

	c, err := platform.DialTemporal(ctx, platform.TemporalConfig{Address: addr, Namespace: "claims"}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	w := worker.New(c, workflow.TaskQueue, worker.Options{})
	workflow.Register(w, &workflow.Activities{})
	if err := w.Start(); err != nil {
		t.Fatalf("starting the worker: %v", err)
	}
	defer w.Stop()

	tx := isolated(t, pool)
	requestCtx, request := requestSpan(t)
	claim := newClaimWith(t, requestCtx, tx)
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

	// The request, the relay's delivery, the start and the workflow run are one trace.
	for _, name := range []string{"relay deliver " + relay.KindWorkflowStart, "StartWorkflow:" + workflow.ClaimWorkflowName,
		"RunWorkflow:" + workflow.ClaimWorkflowName} {
		if !inTrace(spans, name, request.TraceID()) {
			t.Errorf("no %q span in the request's trace %s", name, request.TraceID())
		}
	}

	// A signal for the closed workflow can never be delivered.
	late := relay.Message{EventID: submitted, ClaimID: claim, Kind: relay.KindWorkflowSignal, WorkflowID: id,
		Name: workflow.SignalSubmitted, Payload: fmt.Appendf(nil, `{"claimId":%q}`, claim)}
	if err := temporal.Deliver(ctx, late); !errors.Is(err, relay.ErrUndeliverable) {
		t.Fatalf("signalling the closed workflow: err = %v, want ErrUndeliverable", err)
	}
}

// inTrace reports whether a finished span called name belongs to the trace.
func inTrace(spans *tracetest.SpanRecorder, name string, id trace.TraceID) bool {
	for _, s := range spans.Ended() {
		if s.Name() == name && s.SpanContext().TraceID() == id {
			return true
		}
	}
	return false
}

func status(t *testing.T, c client.Client, id string) enumspb.WorkflowExecutionStatus {
	t.Helper()
	desc, err := c.DescribeWorkflow(context.Background(), id, "")
	if err != nil {
		t.Fatalf("describing %s: %v", id, err)
	}
	return desc.Status
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
