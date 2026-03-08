package executor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bytedance/gopkg/util/logger"

	"github.com/SosisterRapStar/LETI-paper/database"
	"github.com/SosisterRapStar/LETI-paper/internal/inbox"
	"github.com/SosisterRapStar/LETI-paper/internal/observability/metrics"
	"github.com/SosisterRapStar/LETI-paper/internal/observability/tracing"
	"github.com/SosisterRapStar/LETI-paper/internal/outbox"
	"github.com/SosisterRapStar/LETI-paper/message"
	"github.com/SosisterRapStar/LETI-paper/retry"
	"github.com/SosisterRapStar/LETI-paper/step"

	"go.opentelemetry.io/otel/trace"
)

// StepExecutor — атомарная единица "действие + транзакция + outbox".
// Управляет жизненным циклом транзакций и применяет паттерны надёжности (retry).
//
// Содержит два уровня retry:
//   - infraRetrier (опционально) — глобальный, для инфраструктурных ошибок (BeginTx, Commit, WriteOutbox).
//     Если не задан — инфраструктурные операции выполняются без retry.
//   - per-step RetryPolicy — для пользовательских ошибок (action вернул RetryableError).
//
// Инфраструктурные ошибки не расходуют бюджет пользовательского retry.
type StepExecutor struct {
	db             database.DB
	writer         *outbox.Writer
	inbox          *inbox.Inbox
	infraRetrier   *retry.Retrier
	metrics        *metrics.Metrics
	tracingEnabled bool
	tracerName     string
	tracer         trace.Tracer
}

// New создаёт новый StepExecutor.
//   - db — соединение с базой данных для создания транзакций.
//   - w — writer для записи в outbox.
//   - inbox — для дедупликации входящих сообщений (inbox-паттерн); может быть nil — тогда claim не выполняется.
//   - infraRetrier — политика retry для инфраструктурных ошибок (BeginTx, Commit, WriteOutbox).
//     Может быть nil — в этом случае ошибки самой библиотеки не ретраятся
//   - metrics — опциональные метрики саг (Prometheus); если nil — метрики не собираются.
//   - tracingEnabled — включать спэны шагов и inject/extract trace-контекста в сообщения (передаётся из controller.Config.Tracing != nil).
//   - tracerName — имя трассера, когда tracer == nil (из Config.Tracing.TracerName); при выключенном трейсинге может быть пустым.
//   - tracer — свой trace.Tracer (из Config.Tracing.Tracer); если nil, используется глобальный otel.Tracer(tracerName).
func New(db database.DB, w *outbox.Writer, inbox *inbox.Inbox, infraRetrier *retry.Retrier, metrics *metrics.Metrics, tracingEnabled bool, tracerName string, tr trace.Tracer) (*StepExecutor, error) {
	if db == nil {
		return nil, fmt.Errorf("db is required")
	}
	if w == nil {
		return nil, fmt.Errorf("writer is required")
	}
	return &StepExecutor{
		db:             db,
		writer:         w,
		inbox:          inbox,
		infraRetrier:   infraRetrier,
		metrics:        metrics,
		tracingEnabled: tracingEnabled,
		tracerName:     tracerName,
		tracer:         tr,
	}, nil
}

// retryInfra выполняет work с infraRetrier, если он задан.
// Если infraRetrier == nil — выполняет work ровно один раз.
func (e *StepExecutor) retryInfra(ctx context.Context, work func(ctx context.Context) error) error {
	if e.infraRetrier == nil {
		return work(ctx)
	}
	return e.infraRetrier.Retry(ctx, work)
}

// ExecuteStep выполняет шаг саги с полным циклом надёжности:
//  1. Попытка выполнить action (user retry + infra retry), каждая попытка = новая TX.
//  2. Если action провалился и есть ErrorHandler — вызвать его в свежей TX.
//  3. Если ErrorHandler нет или тоже упал — опубликовать failure event.
//
// При успехе возвращает сообщение, которое записано в outbox (результат шага с SagaID, FromStep, MessageType).
// Его можно передать в следующий шаг при последовательном запуске.
func (e *StepExecutor) ExecuteStep(ctx context.Context, stp *step.Step, msg message.Message) (outMsg message.Message, err error) {
	var span trace.Span
	if e.tracingEnabled {
		ctx, span = tracing.StartStepSpan(ctx, e.tracer, e.tracerName, msg.SagaID, stp.Name(), "execute")
	} else {
		span = trace.SpanFromContext(ctx)
	}
	defer func() {
		tracing.EndStepSpan(span, err)
	}()
	if e.metrics != nil {
		start := time.Now()
		defer func() {
			e.metrics.ObserveStep(stp.Name(), "execute", time.Since(start), err)
		}()
	}

	routing := stp.GetRouting()

	outMsg, err = e.runWithUserRetry(ctx, stp, msg, stp.GetExecute(),
		message.EventTypeComplete, routing.NextStepTopics, stp.GetRetryPolicy())
	if err == nil {
		logger.Info("execute action committed, messages written to outbox")
		return outMsg, nil
	}

	errHandler := stp.GetOnError()
	if errHandler == nil {
		return message.Message{}, e.publishEvent(ctx, stp, msg, message.EventTypeFailed, routing.ErrorTopics)
	}

	// Сообщение из OnError уходит в NextStepTopics с типом Complete — сага продолжается.
	outMsg, handlerErr := e.runErrorHandler(ctx, stp, msg, err, errHandler,
		message.EventTypeComplete, routing.NextStepTopics)
	if handlerErr == nil {
		logger.Info("error handler succeeded, continuing saga")
		return outMsg, nil
	}

	// OnError не смог обработать ошибку: в ErrorTopics уходит сообщение. Если handler вернул непустое
	// сообщение (outMsg) — отправляем его, иначе исходное msg.
	msgForError := msg
	if outMsg.SagaID != "" || len(outMsg.Payload) > 0 {
		msgForError = outMsg
		if msgForError.SagaID == "" {
			msgForError.SagaID = msg.SagaID
		}
	}
	logger.Info("error handler failed, sending failure event")
	return message.Message{}, e.publishEvent(ctx, stp, msgForError, message.EventTypeFailed, routing.ErrorTopics)
}

// CompensateStep выполняет компенсацию шага
func (e *StepExecutor) CompensateStep(ctx context.Context, stp *step.Step, msg message.Message) (err error) {
	var span trace.Span
	if e.tracingEnabled {
		ctx, span = tracing.StartStepSpan(ctx, e.tracer, e.tracerName, msg.SagaID, stp.Name(), "compensate")
	} else {
		span = trace.SpanFromContext(ctx)
	}
	defer func() {
		tracing.EndStepSpan(span, err)
	}()
	if e.metrics != nil {
		start := time.Now()
		defer func() {
			e.metrics.ObserveStep(stp.Name(), "compensate", time.Since(start), err)
		}()
	}

	routing := stp.GetRouting()

	_, err = e.runWithUserRetry(ctx, stp, msg, stp.GetCompensate(),
		message.EventTypeFailed, routing.ErrorTopics, stp.GetRetryPolicy())
	if err == nil {
		logger.Info("compensation committed, messages written to outbox")
		return nil
	}

	errHandler := stp.GetOnCompensateError()
	if errHandler == nil {
		logger.Warnf("compensation failed and no error handler configured, sagaID: %s", msg.SagaID)
		return fmt.Errorf("compensation failed: %w", err)
	}

	// Сообщение из OnCompensateError уходит в ErrorTopics с типом Failed — цепочка компенсации продолжается.
	_, handlerErr := e.runErrorHandler(ctx, stp, msg, err, errHandler,
		message.EventTypeFailed, routing.ErrorTopics)
	if handlerErr == nil {
		logger.Info("compensation error handler succeeded, continuing compensation")
		return nil
	}

	logger.Warnf("compensation error handler failed, sagaID: %s", msg.SagaID)
	return fmt.Errorf("compensation error handler failed: %w", handlerErr)
}

// atomicRun — одна попытка выполнения: BeginTx → [inbox claim при from_step] → action → WriteOutbox → Commit.
//
// Классифицирует ошибки:
//   - инфраструктурные (BeginTx, WriteMessages, Commit) → retry.AsRetryable — infraRetrier повторит.
//   - пользовательские (action) → actionError — infraRetrier остановится.
//   - inbox.ErrDuplicate — входящее сообщение уже обработано (inbox), ACK без повторного выполнения.
func (e *StepExecutor) atomicRun(
	ctx context.Context,
	stp *step.Step,
	msg message.Message,
	action step.Action,
	eventType message.MessageType,
	topics []string,
) (message.Message, error) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return message.Message{}, retry.AsRetryable(fmt.Errorf("begin tx: %w", err))
	}
	defer tx.Rollback() //nolint:errcheck

	if e.inbox != nil && msg.FromStep != "" {
		if err := e.inbox.Claim(ctx, tx, msg); err != nil {
			return message.Message{}, err
		}
	}

	result, err := action(ctx, tx, msg)
	if err != nil {
		return message.Message{}, &actionError{err: err}
	}

	result.SagaID = msg.SagaID

	if len(topics) > 0 {
		result.MessageType = eventType
		result.FromStep = stp.Name()
		if e.tracingEnabled {
			tracing.InjectTraceContext(ctx, &result)
		}
		if err := e.writer.WriteMessages(ctx, result, tx, topics, stp.Name()); err != nil {
			return message.Message{}, retry.AsRetryable(fmt.Errorf("outbox write: %w", err))
		}
	}

	if err := tx.Commit(); err != nil {
		return message.Message{}, retry.AsRetryable(fmt.Errorf("commit tx: %w", err))
	}

	return result, nil
}

// runWithInfraRetry оборачивает atomicRun в infraRetrier (если задан).
// infraRetrier повторяет при RetryableError (infra), останавливается на actionError (user).
// Если infraRetrier не задан — atomicRun выполняется ровно один раз.
// Возвращает развёрнутую ошибку: actionError → inner error.
func (e *StepExecutor) runWithInfraRetry(
	ctx context.Context,
	stp *step.Step,
	msg message.Message,
	action step.Action,
	eventType message.MessageType,
	topics []string,
) (message.Message, error) {
	var outMsg message.Message
	err := e.retryInfra(ctx, func(retryCtx context.Context) error {
		var runErr error
		outMsg, runErr = e.atomicRun(retryCtx, stp, msg, action, eventType, topics)
		return runErr
	})
	if err != nil {
		var ae *actionError
		if errors.As(err, &ae) {
			return message.Message{}, ae.Unwrap()
		}
		return message.Message{}, err
	}
	return outMsg, nil
}

// runWithUserRetry — внешний retry-цикл с пользовательской политикой.
// Каждая попытка проходит через runWithInfraRetry (→ atomicRun с новой TX).
// Пользовательский retrier повторяет при RetryableError, останавливается на обычных ошибках.
func (e *StepExecutor) runWithUserRetry(
	ctx context.Context,
	stp *step.Step,
	msg message.Message,
	action step.Action,
	eventType message.MessageType,
	topics []string,
	userRetryPolicy *retry.Retrier,
) (message.Message, error) {
	if userRetryPolicy == nil {
		return e.runWithInfraRetry(ctx, stp, msg, action, eventType, topics)
	}

	var outMsg message.Message
	err := userRetryPolicy.Retry(ctx, func(retryCtx context.Context) error {
		var runErr error
		outMsg, runErr = e.runWithInfraRetry(retryCtx, stp, msg, action, eventType, topics)
		return runErr
	})
	if err != nil {
		return message.Message{}, err
	}
	return outMsg, nil
}

// runErrorHandler вызывает ErrorHandler в свежей TX с infra retry.
// Если handler успешен — возвращённое сообщение (с подставленными SagaID, FromStep, MessageType=eventType)
// записывается в outbox в топики topics и коммитится.
// Если handler упал — возвращается (result, err); result — сообщение, которое вернул handler (можно использовать
// для отправки в error-топик: при непустом result вызывающий код отправит его вместо исходного msg).
func (e *StepExecutor) runErrorHandler(
	ctx context.Context,
	stp *step.Step,
	msg message.Message,
	originalErr error,
	handler step.ErrorHandler,
	eventType message.MessageType,
	topics []string,
) (message.Message, error) {
	var outMsg message.Message
	var handlerResultOnErr message.Message
	err := e.retryInfra(ctx, func(retryCtx context.Context) error {
		tx, txErr := e.db.BeginTx(retryCtx, nil)
		if txErr != nil {
			return retry.AsRetryable(fmt.Errorf("begin error handler tx: %w", txErr))
		}
		defer tx.Rollback() //nolint:errcheck

		if e.inbox != nil && msg.FromStep != "" {
			if claimErr := e.inbox.Claim(retryCtx, tx, msg); claimErr != nil {
				return claimErr
			}
		}

		result, handlerErr := handler(retryCtx, tx, msg, originalErr)
		if handlerErr != nil {
			handlerResultOnErr = result
			return &actionError{err: handlerErr}
		}

		result.SagaID = msg.SagaID

		if len(topics) > 0 {
			result.MessageType = eventType
			result.FromStep = stp.Name()
			if e.tracingEnabled {
				tracing.InjectTraceContext(retryCtx, &result)
			}
			if writeErr := e.writer.WriteMessages(retryCtx, result, tx, topics, stp.Name()); writeErr != nil {
				return retry.AsRetryable(fmt.Errorf("outbox write: %w", writeErr))
			}
		}

		if commitErr := tx.Commit(); commitErr != nil {
			return retry.AsRetryable(fmt.Errorf("commit error handler tx: %w", commitErr))
		}

		outMsg = result
		return nil
	})
	if err != nil {
		var ae *actionError
		if errors.As(err, &ae) {
			return handlerResultOnErr, ae.Unwrap()
		}
		return message.Message{}, err
	}
	return outMsg, nil
}

// publishEvent записывает событие в outbox через отдельную транзакцию с infra retry.
func (e *StepExecutor) publishEvent(
	ctx context.Context,
	stp *step.Step,
	msg message.Message,
	eventType message.MessageType,
	topics []string,
) error {
	if len(topics) == 0 {
		logger.Infof("no topics configured for event type %s", eventType)
		return nil
	}

	msg.MessageType = eventType
	msg.FromStep = stp.Name()

	return e.retryInfra(ctx, func(retryCtx context.Context) error {
		if err := e.writer.WriteTx(retryCtx, msg, topics, stp.Name(), nil); err != nil {
			return retry.AsRetryable(fmt.Errorf("publish event [type=%s]: %w", eventType, err))
		}
		return nil
	})
}
