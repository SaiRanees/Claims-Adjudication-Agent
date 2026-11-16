package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
)

// Temporal delivers messages with a Temporal client: a start begins the claim's workflow
// and a signal goes to it. Either way the workflow receives the row's payload with the
// row's event ID added as eventId, which lets it ignore a message delivered twice.
type Temporal struct {
	client    client.Client
	taskQueue string
}

// NewTemporal returns a Deliverer that starts workflows on taskQueue.
func NewTemporal(c client.Client, taskQueue string) *Temporal {
	return &Temporal{client: c, taskQueue: taskQueue}
}

// Deliver delivers one message. Delivering a start again is harmless: it finds the
// claim's workflow, running or finished, and never begins a second one.
func (d *Temporal) Deliver(ctx context.Context, m Message) error {
	arg, err := withEventID(m.Payload, m.EventID)
	if err != nil {
		// A malformed payload fails the same way on every retry.
		return fmt.Errorf("%w: %w", ErrUndeliverable, err)
	}
	switch m.Kind {
	case KindWorkflowStart:
		_, err := d.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
			ID:        m.WorkflowID,
			TaskQueue: d.taskQueue,
			// A claim has one workflow, ever (ADR-0002). Temporal's default would let
			// a start with the ID of a finished workflow begin a second run.
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		}, m.Name, arg)
		if temporal.IsWorkflowExecutionAlreadyStartedError(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("starting workflow %s: %w", m.WorkflowID, err)
		}
		return nil
	case KindWorkflowSignal:
		err := d.client.SignalWorkflow(ctx, m.WorkflowID, "", m.Name, arg)
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			// The relay delivers a claim's start before its signals, so a workflow
			// that can't be found has already closed.
			return fmt.Errorf("%w: workflow %s has closed: %w", ErrUndeliverable, m.WorkflowID, err)
		}
		if err != nil {
			return fmt.Errorf("signalling workflow %s: %w", m.WorkflowID, err)
		}
		return nil
	}
	return fmt.Errorf("%w: unknown kind %q", ErrUndeliverable, m.Kind)
}

// withEventID returns the payload's fields with eventId added.
func withEventID(payload []byte, eventID uuid.UUID) (map[string]json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, fmt.Errorf("the payload isn't a JSON object: %w", err)
	}
	id, err := json.Marshal(eventID.String())
	if err != nil {
		return nil, err
	}
	fields["eventId"] = id
	return fields, nil
}
