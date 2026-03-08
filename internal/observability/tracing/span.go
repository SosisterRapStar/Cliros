package tracing

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultTracerName  = "saga"
	spanNameExecute    = "saga.step.execute"
	spanNameCompensate = "saga.step.compensate"
	spanNameStart      = "saga.start"
)

func resolveTracerName(name string) string {
	if name != "" {
		return name
	}
	return defaultTracerName
}

func getTracer(tr trace.Tracer, tracerName string) trace.Tracer {
	if tr != nil {
		return tr
	}
	return otel.Tracer(resolveTracerName(tracerName))
}

func StartStepSpan(ctx context.Context, tr trace.Tracer, tracerName, sagaID, stepName, operation string) (context.Context, trace.Span) {
	return getTracer(tr, tracerName).Start(ctx, spanName(operation),
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("saga.id", sagaID),
			attribute.String("step.name", stepName),
			attribute.String("step.operation", operation),
		))
}

func spanName(operation string) string {
	switch operation {
	case "compensate":
		return spanNameCompensate
	default:
		return spanNameExecute
	}
}

func EndStepSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

func StartSagaSpan(ctx context.Context, tr trace.Tracer, tracerName, sagaID string) (context.Context, trace.Span) {
	return getTracer(tr, tracerName).Start(ctx, spanNameStart,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attribute.String("saga.id", sagaID)))
}
