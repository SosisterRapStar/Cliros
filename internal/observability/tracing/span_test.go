package tracing

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestStartStepSpan_nilTracer_returnsCtxAndSpan(t *testing.T) {
	ctx := context.Background()
	newCtx, sp := StartStepSpan(ctx, nil, "saga-pkg", "saga-1", "step1", "execute")
	if newCtx == nil {
		t.Error("StartStepSpan: ctx is nil")
	}
	if sp == nil {
		t.Fatal("StartStepSpan: span is nil")
	}
	sp.End()
}

func TestStartStepSpan_customTracer_returnsCtxAndSpan(t *testing.T) {
	tr := trace.NewNoopTracerProvider().Tracer("test")
	ctx := context.Background()
	newCtx, sp := StartStepSpan(ctx, tr, "", "saga-1", "step1", "execute")
	if newCtx == nil || sp == nil {
		t.Errorf("StartStepSpan: ctx=%v span=%v", newCtx, sp)
	}
	sp.End()
}

func TestStartStepSpan_compensate_noPanic(t *testing.T) {
	ctx := context.Background()
	_, sp := StartStepSpan(ctx, nil, "", "saga-1", "step1", "compensate")
	if sp == nil {
		t.Fatal("span is nil")
	}
	sp.End()
}

func TestEndStepSpan_withError_noPanic(t *testing.T) {
	ctx := context.Background()
	_, sp := StartStepSpan(ctx, nil, "", "saga-1", "step1", "execute")
	EndStepSpan(sp, errors.New("step failed"))
}

func TestEndStepSpan_nilError_noPanic(t *testing.T) {
	ctx := context.Background()
	_, sp := StartStepSpan(ctx, nil, "", "saga-1", "step1", "execute")
	EndStepSpan(sp, nil)
}

func TestStartSagaSpan_returnsCtxAndSpan(t *testing.T) {
	ctx := context.Background()
	newCtx, sp := StartSagaSpan(ctx, nil, "", "saga-1")
	if newCtx == nil || sp == nil {
		t.Errorf("StartSagaSpan: ctx=%v span=%v", newCtx, sp)
	}
	sp.End()
}

func TestStartSagaSpan_customTracer(t *testing.T) {
	tr := trace.NewNoopTracerProvider().Tracer("test")
	ctx := context.Background()
	_, sp := StartSagaSpan(ctx, tr, "", "saga-1")
	if sp == nil {
		t.Fatal("StartSagaSpan: span is nil")
	}
	sp.End()
}
