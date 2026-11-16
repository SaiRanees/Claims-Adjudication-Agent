package claims_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/claims"
)

func TestCreateStoresTheRequestsTraceOnTheOutboxRow(t *testing.T) {
	const (
		traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID  = "00f067aa0ba902b7"
	)
	tid, _ := trace.TraceIDFromHex(traceID)
	sid, _ := trace.SpanIDFromHex(spanID)
	traced := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	}))
	tests := []struct {
		name string
		ctx  context.Context
		want *string
	}{
		{name: "a traced request", ctx: traced, want: ptr("00-" + traceID + "-" + spanID + "-01")},
		{name: "no trace stores NULL", ctx: context.Background(), want: nil},
	}
	pool := connect(t)
	store := claims.NewStore(pool)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recorder{}
			res, err := store.Create(tt.ctx, request(newPrincipal(), "k1", "body", draft(), rec.render))
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var got *string
			if err := pool.QueryRow(context.Background(),
				`SELECT traceparent FROM outbox WHERE claim_id = $1`, res.ClaimID).Scan(&got); err != nil {
				t.Fatalf("reading the outbox row: %v", err)
			}
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("traceparent = %v, want %v", deref(got), deref(tt.want))
			}
		})
	}
}

func ptr(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}
