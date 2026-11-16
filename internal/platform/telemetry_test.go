package platform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/SaiRanees/Claims-Adjudication-Agent/internal/platform"
)

const (
	traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	spanID      = "00f067aa0ba902b7"
	traceparent = "00-" + traceID + "-" + spanID + "-01"
)

// spanContext returns ctx holding the span that traceparent names.
func spanContext(t *testing.T) context.Context {
	t.Helper()
	tid, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := trace.SpanIDFromHex(spanID)
	if err != nil {
		t.Fatal(err)
	}
	return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	}))
}

func TestNewLoggerAddsTheSpanToEachRecord(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		with        bool // log through logger.With, which wraps the handler again
		wantTraceID string
		wantSpanID  string
	}{
		{name: "with a span", ctx: spanContext(t), wantTraceID: traceID, wantSpanID: spanID},
		{name: "with a span, through With", ctx: spanContext(t), with: true, wantTraceID: traceID, wantSpanID: spanID},
		{name: "without a span", ctx: context.Background()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := platform.NewLogger(&buf, slog.LevelInfo)
			if tt.with {
				log = log.With("claimId", "c-1")
			}
			log.InfoContext(tt.ctx, "hello")
			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("not one JSON record: %q", buf.String())
			}
			if got, _ := rec["trace_id"].(string); got != tt.wantTraceID {
				t.Errorf("trace_id = %q, want %q", got, tt.wantTraceID)
			}
			if got, _ := rec["span_id"].(string); got != tt.wantSpanID {
				t.Errorf("span_id = %q, want %q", got, tt.wantSpanID)
			}
		})
	}
}

func TestNewLoggerKeepsTheLevel(t *testing.T) {
	var buf bytes.Buffer
	platform.NewLogger(&buf, slog.LevelWarn).InfoContext(spanContext(t), "quiet")
	if buf.Len() != 0 {
		t.Errorf("an info record passed a warn logger: %q", buf.String())
	}
}

func TestTraceparent(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want string // "" means nil
	}{
		{name: "a span gives its traceparent", ctx: spanContext(t), want: traceparent},
		{name: "no span gives nil, stored as NULL", ctx: context.Background()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := platform.Traceparent(tt.ctx)
			switch {
			case tt.want == "" && got != nil:
				t.Errorf("Traceparent = %q, want nil", *got)
			case tt.want != "" && (got == nil || *got != tt.want):
				t.Errorf("Traceparent = %v, want %q", got, tt.want)
			}
		})
	}
}

func TestContextWithTraceparent(t *testing.T) {
	tests := []struct {
		name        string
		traceparent string
		wantTraceID string // "" means no span
	}{
		{name: "continues a valid traceparent", traceparent: traceparent, wantTraceID: traceID},
		{name: "ignores an empty one", traceparent: ""},
		{name: "ignores a malformed one", traceparent: "00-not-a-trace-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := trace.SpanContextFromContext(platform.ContextWithTraceparent(context.Background(), tt.traceparent))
			if tt.wantTraceID == "" {
				if sc.IsValid() {
					t.Errorf("span context = %v, want none", sc)
				}
				return
			}
			if !sc.IsRemote() || sc.TraceID().String() != tt.wantTraceID || sc.SpanID().String() != spanID {
				t.Errorf("span context = %v, want remote %s/%s", sc, tt.wantTraceID, spanID)
			}
		})
	}
}

func TestStartTelemetryWithoutAnEndpointStillGivesTraceIDs(t *testing.T) {
	t.Cleanup(func() { otel.SetTracerProvider(noop.NewTracerProvider()) })
	stop, err := platform.StartTelemetry(context.Background(), platform.TelemetryConfig{ServiceName: "test"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("StartTelemetry: %v", err)
	}
	defer stop()

	ctx, span := otel.Tracer("test").Start(context.Background(), "work")
	defer span.End()
	if !span.SpanContext().IsValid() || !span.SpanContext().IsSampled() {
		t.Fatalf("span context = %v, want a valid sampled span", span.SpanContext())
	}
	if platform.Traceparent(ctx) == nil {
		t.Error("Traceparent = nil for a live span")
	}
}
