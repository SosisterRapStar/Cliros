package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	labelStep      = "step"
	labelOperation = "operation"
	labelStatus    = "status"
	statusSuccess  = "success"
	statusFailure  = "failure"
)

// Metrics собирает базовые метрики по сагам и шагам для экспорта в Prometheus.
type Metrics struct {
	StepsTotal   *prometheus.CounterVec
	StepDuration *prometheus.HistogramVec
	SagaStarted  prometheus.Counter
}

// New создаёт Metrics, регистрирует все метрики в registry и возвращает объект.
// subsystem задаёт префикс имён (например "saga"). Если registry == nil — возвращается (nil, nil);
// такой результат можно передать в Config.Metrics (метрики будут отключены).
func New(registry *prometheus.Registry, subsystem string) (*Metrics, error) {
	if registry == nil {
		return nil, nil
	}

	m := &Metrics{
		StepsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Subsystem: subsystem,
				Name:      "steps_total",
				Help:      "Total number of saga steps executed by operation and status",
			},
			[]string{labelStep, labelOperation, labelStatus},
		),
		StepDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Subsystem: subsystem,
				Name:      "step_duration_seconds",
				Help:      "Duration of saga step execution in seconds",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{labelStep, labelOperation},
		),
		SagaStarted: prometheus.NewCounter(
			prometheus.CounterOpts{
				Subsystem: subsystem,
				Name:      "started_total",
				Help:      "Total number of sagas started (only for synchronous StartSaga/StartSagaWithSteps)",
			},
		),
	}

	for _, col := range []prometheus.Collector{m.StepsTotal, m.StepDuration, m.SagaStarted} {
		if err := registry.Register(col); err != nil {
			return nil, err
		}
	}

	return m, nil
}

// ObserveStep записывает результат выполнения шага: инкремент счётчика по статусу и наблюдение длительности.
// No-op если m == nil.
func (m *Metrics) ObserveStep(step, operation string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	status := statusSuccess
	if err != nil {
		status = statusFailure
	}
	m.StepsTotal.WithLabelValues(step, operation, status).Inc()
	m.StepDuration.WithLabelValues(step, operation).Observe(duration.Seconds())
}

// SagaStartedInc инкрементирует счётчик запущенных саг (только при вызове StartSaga/StartSagaWithSteps). No-op если m == nil.
func (m *Metrics) SagaStartedInc() {
	if m == nil {
		return
	}
	m.SagaStarted.Inc()
}
