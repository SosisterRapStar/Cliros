package tracing

import (
	"context"

	"github.com/SosisterRapStar/cliros/message"
	"go.opentelemetry.io/otel"
)

func InjectTraceContext(ctx context.Context, msg *message.Message) {
	if msg == nil {
		return
	}
	carrier := MessageCarrier{Msg: msg}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
}

func ExtractTraceContext(ctx context.Context, msg *message.Message) context.Context {
	if msg == nil {
		return ctx
	}
	carrier := MessageCarrier{Msg: msg}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
