package tracing

import (
	"context"

	"github.com/SosisterRapStar/LETI-paper/message"
	"go.opentelemetry.io/otel"
)

// InjectTraceContext записывает текущий trace-контекст из ctx в msg (Traceparent, Tracestate).
// Вызывать перед записью сообщения в outbox, чтобы следующий consumer мог продолжить тот же трейс.
func InjectTraceContext(ctx context.Context, msg *message.Message) {
	if msg == nil {
		return
	}
	carrier := MessageCarrier{Msg: msg}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
}

// ExtractTraceContext извлекает trace-контекст из msg в ctx и возвращает новый context.
// Вызывать в начале обработки входящего сообщения (например в Register), чтобы ExecuteStep/CompensateStep
// создавали спэны как дочерние к трейсу отправителя.
func ExtractTraceContext(ctx context.Context, msg *message.Message) context.Context {
	if msg == nil {
		return ctx
	}
	carrier := MessageCarrier{Msg: msg}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
