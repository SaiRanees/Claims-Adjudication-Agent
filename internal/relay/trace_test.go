package relay_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/relay"
)

// recordSpans installs a tracer provider that keeps every finished span, for this test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(noop.NewTracerProvider())
		_ = provider.Shutdown(context.Background())
	})
	return spans
}

// requestSpan is the API request that created a claim, as the claim's outbox row records it.
func requestSpan(t *testing.T) (context.Context, trace.SpanContext) {
	t.Helper()
	ctx, span := otel.Tracer("test").Start(context.Background(), "POST /v1/claims")
	span.End()
	return ctx, span.SpanContext()
}

// spanFake records the span each delivery was made in, and fails with err when set.
type spanFake struct {
	mu    sync.Mutex
	spans []trace.SpanContext
	err   error
}

func (f *spanFake) Deliver(ctx context.Context, _ relay.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spans = append(f.spans, trace.SpanContextFromContext(ctx))
	return f.err
}

func TestDeliveryContinuesTheTraceThatQueuedTheMessage(t *testing.T) {
	tests := []struct {
		name       string
		traced     bool  // the claim was created inside a request span
		deliverErr error // what Temporal answers
		wantStatus codes.Code
	}{
		{name: "a traced message joins its request's trace", traced: true, wantStatus: codes.Unset},
		{name: "a failed delivery is marked as an error", traced: true, deliverErr: errors.New("temporal is down"), wantStatus: codes.Error},
		{name: "a message without a trace starts a new one", traced: false, wantStatus: codes.Unset},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := recordSpans(t)
			tx := isolated(t, connect(t))
			ctx, parent := context.Background(), trace.SpanContext{}
			if tt.traced {
				ctx, parent = requestSpan(t)
			}
			claim := newClaimWith(t, ctx, tx)
			f := &spanFake{err: tt.deliverErr}
			if !deliverNext(t, relay.New(tx, f, quiet)) {
				t.Fatal("DeliverNext found nothing to deliver")
			}

			got := deliverySpan(t, spans)
			if tt.traced {
				if got.SpanContext().TraceID() != parent.TraceID() || got.Parent().SpanID() != parent.SpanID() {
					t.Errorf("delivery span %s has parent %s, want a child of %s",
						got.SpanContext().SpanID(), got.Parent().SpanID(), parent.SpanID())
				}
			} else if got.Parent().IsValid() {
				t.Errorf("delivery span has parent %v, want a new root", got.Parent())
			}
			if len(f.spans) != 1 || !f.spans[0].Equal(got.SpanContext()) {
				t.Errorf("Temporal was called in span %v, want the delivery span %v", f.spans, got.SpanContext())
			}
			if got.Status().Code != tt.wantStatus {
				t.Errorf("status = %v, want %v", got.Status().Code, tt.wantStatus)
			}
			if !hasAttr(got.Attributes(), attribute.String("claim.id", claim.String())) {
				t.Errorf("attributes %v don't name claim %s", got.Attributes(), claim)
			}
		})
	}
}

// deliverySpan returns the one finished relay span.
func deliverySpan(t *testing.T, spans *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	var found []sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.Name() == "relay deliver "+relay.KindWorkflowStart {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%d delivery spans, want 1", len(found))
	}
	return found[0]
}

func hasAttr(attrs []attribute.KeyValue, want attribute.KeyValue) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}
