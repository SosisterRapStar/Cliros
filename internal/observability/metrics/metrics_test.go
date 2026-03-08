package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNew_nilRegistry_returnsNil(t *testing.T) {
	m, err := New(nil, "saga")
	if err != nil {
		t.Fatalf("New(nil, \"saga\"): err=%v", err)
	}
	if m != nil {
		t.Errorf("New(nil, \"saga\"): got %v, want nil", m)
	}
}

func TestNew_registersAndRecords(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := New(reg, "saga")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("New: got nil Metrics")
	}

	m.ObserveStep("step1", "execute", 100*time.Millisecond, nil)
	m.ObserveStep("step1", "execute", 50*time.Millisecond, errFake)
	m.SagaStartedInc()
	m.SagaStartedInc()

	got, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	var stepsTotal, stepDuration, startedTotal float64
	for _, mf := range got {
		for _, m := range mf.GetMetric() {
			switch mf.GetName() {
			case "saga_steps_total":
				stepsTotal += m.GetCounter().GetValue()
			case "saga_step_duration_seconds":
				stepDuration += float64(m.GetHistogram().GetSampleCount())
			case "saga_started_total":
				startedTotal += m.GetCounter().GetValue()
			}
		}
	}

	if stepsTotal != 2 {
		t.Errorf("saga_steps_total: got %v, want 2", stepsTotal)
	}
	if stepDuration != 2 {
		t.Errorf("saga_step_duration_seconds sample count: got %v, want 2", stepDuration)
	}
	if startedTotal != 2 {
		t.Errorf("saga_started_total: got %v, want 2", startedTotal)
	}
}

func TestObserveStep_nilMetrics_noop(t *testing.T) {
	var m *Metrics
	m.ObserveStep("x", "execute", time.Second, nil)
}

func TestSagaStartedInc_nilMetrics_noop(t *testing.T) {
	var m *Metrics
	m.SagaStartedInc()
}

var errFake = errT{}

type errT struct{}

func (errT) Error() string { return "fake" }
