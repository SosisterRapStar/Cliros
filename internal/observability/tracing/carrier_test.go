package tracing

import (
	"testing"

	"github.com/SosisterRapStar/LETI-paper/message"
	"go.opentelemetry.io/otel/propagation"
)

func TestMessageCarrier_GetSet_nilMsg(t *testing.T) {
	c := MessageCarrier{Msg: nil}
	if g := c.Get(keyTraceparent); g != "" {
		t.Errorf("Get(traceparent) = %q, want \"\"", g)
	}
	c.Set(keyTraceparent, "v")
	c.Set(keyTracestate, "s")
}

func TestMessageCarrier_GetSet_roundtrip(t *testing.T) {
	msg := &message.Message{}
	c := MessageCarrier{Msg: msg}

	c.Set(keyTraceparent, "00-trace-id- span-id-01")
	c.Set(keyTracestate, "k=v")

	if g := c.Get(keyTraceparent); g != "00-trace-id- span-id-01" {
		t.Errorf("Get(traceparent) = %q", g)
	}
	if g := c.Get(keyTracestate); g != "k=v" {
		t.Errorf("Get(tracestate) = %q", g)
	}
	if msg.Traceparent != "00-trace-id- span-id-01" || msg.Tracestate != "k=v" {
		t.Errorf("message fields: traceparent=%q tracestate=%q", msg.Traceparent, msg.Tracestate)
	}
}

func TestMessageCarrier_Get_unknownKey(t *testing.T) {
	msg := &message.Message{}
	c := MessageCarrier{Msg: msg}
	if g := c.Get("unknown"); g != "" {
		t.Errorf("Get(unknown) = %q, want \"\"", g)
	}
}

func TestMessageCarrier_Keys(t *testing.T) {
	c := MessageCarrier{Msg: &message.Message{}}
	keys := c.Keys()
	if len(keys) != 2 {
		t.Fatalf("Keys() len = %d, want 2", len(keys))
	}
	m := make(map[string]bool)
	for _, k := range keys {
		m[k] = true
	}
	if !m[keyTraceparent] || !m[keyTracestate] {
		t.Errorf("Keys() = %v", keys)
	}
}

func TestMessageCarrier_implementsTextMapCarrier(t *testing.T) {
	var _ propagation.TextMapCarrier = MessageCarrier{Msg: &message.Message{}}
}
