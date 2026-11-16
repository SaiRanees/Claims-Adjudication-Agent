package platform

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.temporal.io/sdk/client"
	temporalotel "go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	temporallog "go.temporal.io/sdk/log"
)

// flushTimeout bounds the export of buffered spans at exit.
const flushTimeout = 5 * time.Second

// Propagator carries trace context between processes: the W3C traceparent header only.
// Baggage is left out on purpose, because traces carry IDs and never content.
var Propagator propagation.TextMapPropagator = propagation.TraceContext{}

// StartTelemetry installs the process's tracer provider and propagator as the OTEL
// globals. Every request, outbox delivery and workflow gets a sampled span, so logs and
// outbox rows always have a trace ID; spans are exported over OTLP/HTTP only when
// cfg.OTLPEndpoint is set. Call the returned stop before exiting: it flushes what is
// still buffered, giving up after flushTimeout so a missing collector can't hold up the
// exit.
func StartTelemetry(ctx context.Context, cfg TelemetryConfig, log *slog.Logger) (stop func(), err error) {
	res, err := resource.Merge(resource.Default(),
		resource.NewSchemaless(attribute.String("service.name", cfg.ServiceName)))
	if err != nil {
		return nil, fmt.Errorf("describing the service: %w", err)
	}
	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
	}
	if cfg.OTLPEndpoint != "" {
		exporter, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(strings.TrimRight(cfg.OTLPEndpoint, "/")+"/v1/traces"))
		if err != nil {
			return nil, fmt.Errorf("creating the OTLP exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exporter))
	}
	provider := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(Propagator)
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		log.Warn("telemetry failed", "error", err)
	}))
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			log.Warn("flushing telemetry", "error", err)
		}
	}, nil
}

// NewLogger returns the services' JSON logger. A record logged with a context that
// holds a span carries trace_id and span_id, so logs and traces join up in Grafana.
func NewLogger(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(traceHandler{slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})})
}

// traceHandler adds the span in the record's context to the record.
type traceHandler struct{ slog.Handler }

func (h traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r = r.Clone()
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceHandler) WithGroup(name string) slog.Handler {
	return traceHandler{h.Handler.WithGroup(name)}
}

// DialTemporal connects to Temporal with tracing on. Workflow starts and signals carry
// the caller's span in their headers, and a worker made from this client continues it,
// so a claim's workflow joins the trace of the request that created the claim.
func DialTemporal(ctx context.Context, cfg TemporalConfig, log *slog.Logger) (client.Client, error) {
	tracing, err := TemporalTracing()
	if err != nil {
		return nil, err
	}
	tc, err := client.DialContext(ctx, client.Options{
		HostPort:     cfg.Address,
		Namespace:    cfg.Namespace,
		Logger:       temporallog.NewStructuredLogger(log),
		Interceptors: []interceptor.ClientInterceptor{tracing},
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to Temporal: %w", err)
	}
	return tc, nil
}

// TemporalTracing is Temporal's OpenTelemetry interceptor on the global tracer provider
// and Propagator. It is both a client and a worker interceptor.
func TemporalTracing() (interceptor.Interceptor, error) {
	tracing, err := temporalotel.NewTracingInterceptor(temporalotel.TracerOptions{
		Tracer:            otel.GetTracerProvider().Tracer("go.temporal.io/sdk"),
		TextMapPropagator: Propagator,
		DisableBaggage:    true,
	})
	if err != nil {
		return nil, fmt.Errorf("creating the Temporal tracing interceptor: %w", err)
	}
	return tracing, nil
}

// Traceparent is ctx's span as a W3C traceparent value, for an outbox row to carry
// across to the relay. It is nil when ctx holds no span, which stores NULL.
func Traceparent(ctx context.Context) *string {
	carrier := propagation.MapCarrier{}
	Propagator.Inject(ctx, carrier)
	if v := carrier.Get("traceparent"); v != "" {
		return &v
	}
	return nil
}

// ContextWithTraceparent continues the trace that traceparent names: spans started from
// the returned context are its children. An empty or malformed value leaves ctx as it
// is, so the next span starts a new trace.
func ContextWithTraceparent(ctx context.Context, traceparent string) context.Context {
	return Propagator.Extract(ctx, propagation.MapCarrier{"traceparent": traceparent})
}
