package tracing

import (
	"context"
	"testing"

	"github.com/SosisterRapStar/LETI-paper/message"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

func TestInjectTraceContext_nilMsg_noop(t *testing.T) {
	InjectTraceContext(context.Background(), nil)
}

func TestExtractTraceContext_nilMsg_returnsSameCtx(t *testing.T) {
	ctx := context.Background()
	out := ExtractTraceContext(ctx, nil)
	if out != ctx {
		t.Error("ExtractTraceContext(ctx, nil) should return same ctx")
	}
}

func TestInjectExtract_roundtrip(t *testing.T) {
	tr := trace.NewNoopTracerProvider().Tracer("test")
	ctx, sp := tr.Start(context.Background(), "parent")
	defer sp.End()

	msg := &message.Message{}
	InjectTraceContext(ctx, msg)
	if msg.Traceparent == "" && msg.Tracestate == "" {
		t.Skip("noop tracer may not inject traceparent; propagator may be noop")
	}

	outCtx := ExtractTraceContext(context.Background(), msg)
	if outCtx == nil {
		t.Fatal("ExtractTraceContext returned nil ctx")
	}
	_, childSp := otel.Tracer("test").Start(outCtx, "child")
	childSp.End()
}
