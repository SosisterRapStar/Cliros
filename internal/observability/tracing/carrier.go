package tracing

import (
	"github.com/SosisterRapStar/cliros/message"
	"go.opentelemetry.io/otel/propagation"
)

const (
	keyTraceparent = "traceparent"
	keyTracestate  = "tracestate"
)

type MessageCarrier struct {
	Msg *message.Message
}

func (c MessageCarrier) Get(key string) string {
	if c.Msg == nil {
		return ""
	}
	switch key {
	case keyTraceparent:
		return c.Msg.Traceparent
	case keyTracestate:
		return c.Msg.Tracestate
	default:
		return ""
	}
}

func (c MessageCarrier) Set(key string, value string) {
	if c.Msg == nil {
		return
	}
	switch key {
	case keyTraceparent:
		c.Msg.Traceparent = value
	case keyTracestate:
		c.Msg.Tracestate = value
	}
}

func (c MessageCarrier) Keys() []string {
	return []string{keyTraceparent, keyTracestate}
}

var _ propagation.TextMapCarrier = MessageCarrier{}
