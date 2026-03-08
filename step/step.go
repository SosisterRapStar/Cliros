package step

import (
	"context"
	"fmt"

	"github.com/SosisterRapStar/LETI-paper/database"
	"github.com/SosisterRapStar/LETI-paper/message"
	"github.com/SosisterRapStar/LETI-paper/retry"
)

// Action -- пользовательский хэндлер для выполнения или компенсации шага саги.
// tx -- транзакция, в которой выполняется бизнес-логика и запись в outbox атомарно.
//
// Если action хочет, чтобы при ошибке вызов был повторён (при наличии RetryPolicy) —
// ошибку нужно обернуть через retry.AsRetryable(err).
// Не-retryable ошибки прекращают повторы и передаются в ErrorHandler.
type Action func(ctx context.Context, tx database.TxQueryer, msg message.Message) (message.Message, error)

// ErrorHandler — обработчик ошибок, вызывается executor'ом при падении Execute или Compensate
// после того, как все повторные попытки (если были) исчерпаны.
// tx создаётся executor'ом — каждый вызов получает свежую транзакцию.
//
// Возвращённое сообщение (можно пересобрать payload) записывается в outbox и уходит в топики:
//   - OnError при успехе: результат пишется в NextStepTopics с типом "execute" — сага продолжается.
//   - OnError при ошибке: возвращённое сообщение (если не пустое) уходит в ErrorTopics с типом "failed";
//     иначе в ErrorTopics уходит исходное msg.
//   - OnCompensateError: результат пишется в ErrorTopics с типом "failed" — цепочка компенсации продолжается.
type ErrorHandler func(ctx context.Context, tx database.TxQueryer, msg message.Message, err error) (message.Message, error)

type StepParams struct {
	Name              string
	Execute           Action
	Compensate        Action
	Routing           RoutingConfig
	RetryPolicy       *retry.Retrier
	OnError           ErrorHandler
	OnCompensateError ErrorHandler
}

// Step — описание шага саги: бизнес-логика, компенсация, роутинг, retry-политика.
// Является контейнером данных. Управление транзакциями и retry выполняет executor.
type Step struct {
	name              string
	execute           Action
	compensate        Action
	routing           RoutingConfig
	retryPolicy       *retry.Retrier
	onError           ErrorHandler
	onCompensateError ErrorHandler
}

// WithRetry оборачивает пользовательский ErrorHandler в retry-логику.
// handler повторяется при retry.RetryableError в рамках одной транзакции.
// Полезно для ретрая транзиентных не-DB ошибок (внешний API и т.п.).
func WithRetry(r *retry.Retrier, handler ErrorHandler) ErrorHandler {
	return func(ctx context.Context, tx database.TxQueryer, msg message.Message, originalErr error) (message.Message, error) {
		var result message.Message
		retryErr := r.Retry(ctx, func(innerCtx context.Context) error {
			res, handlerErr := handler(innerCtx, tx, msg, originalErr)
			if handlerErr != nil {
				return handlerErr
			}
			result = res
			return nil
		})
		return result, retryErr
	}
}

func New(p *StepParams) (*Step, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("step name is required")
	}

	return &Step{
		name:              p.Name,
		execute:           p.Execute,
		compensate:        p.Compensate,
		routing:           p.Routing,
		retryPolicy:       p.RetryPolicy,
		onError:           p.OnError,
		onCompensateError: p.OnCompensateError,
	}, nil
}

func (s *Step) Name() string {
	return s.name
}

func (s *Step) GetRouting() RoutingConfig {
	return s.routing
}

func (s *Step) GetExecute() Action {
	return s.execute
}

func (s *Step) GetCompensate() Action {
	return s.compensate
}

func (s *Step) GetOnError() ErrorHandler {
	return s.onError
}

func (s *Step) GetOnCompensateError() ErrorHandler {
	return s.onCompensateError
}

func (s *Step) GetRetryPolicy() *retry.Retrier {
	return s.retryPolicy
}
