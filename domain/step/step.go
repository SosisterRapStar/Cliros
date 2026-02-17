package step

import (
	"context"
	"fmt"

	"github.com/SosisterRapStar/LETI-paper/domain/databases"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/SosisterRapStar/LETI-paper/thirdparty/retrier"
)

// Action -- пользовательский хэндлер для выполнения или компенсации шага саги.
// tx -- транзакция, в которой выполняется бизнес-логика и запись в outbox атомарно.
type Action func(ctx context.Context, tx databases.TxQueryer, msg message.Message) (message.Message, error)

// ErrorHandler -- обработчик ошибок, вызывается при падении Execute или Compensate.
// Позволяет пользователю решить, что делать с ошибкой (retry, transform, etc).
type ErrorHandler func(ctx context.Context, msg message.Message, err error) (message.Message, error)

type IDFunc func() string

type StepParams struct {
	Name              string
	Execute           Action
	Compensate        Action
	Routing           RoutingConfig
	RetryPolicy       *retrier.Retrier
	OnError           ErrorHandler
	OnCompensateError ErrorHandler
}

type Step struct {
	name              string
	execute           Action
	compensate        Action
	routing           RoutingConfig
	retryPolicy       *retrier.Retrier
	onError           ErrorHandler
	onCompensateError ErrorHandler
}

// defaultRetryErrorHandler возвращает ErrorHandler по умолчанию,
// который при вызове ретраит переданный action с помощью retrier.
// Замыкается над конкретным action (execute или compensate) и retrier.
// TODO: сейчас tx передаётся как nil — требуется решение по управлению транзакциями.
func defaultRetryErrorHandler(r *retrier.Retrier, action Action) ErrorHandler {
	return func(ctx context.Context, msg message.Message, err error) (message.Message, error) {
		var result message.Message
		retryErr := r.Retry(ctx, func(innerCtx context.Context) error {
			res, actionErr := action(innerCtx, nil, msg)
			if actionErr != nil {
				return retrier.AsRetryable(actionErr)
			}
			result = res
			return nil
		})
		return result, retryErr
	}
}

func New(p *StepParams) (*Step, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("Step name is required")
	}

	onError := p.OnError
	if onError == nil && p.RetryPolicy != nil {
		onError = defaultRetryErrorHandler(p.RetryPolicy, p.Execute)
	}

	onCompensateError := p.OnCompensateError
	if onCompensateError == nil && p.RetryPolicy != nil {
		onCompensateError = defaultRetryErrorHandler(p.RetryPolicy, p.Compensate)
	}

	return &Step{
		name:              p.Name,
		execute:           p.Execute,
		compensate:        p.Compensate,
		routing:           p.Routing,
		retryPolicy:       p.RetryPolicy,
		onError:           onError,
		onCompensateError: onCompensateError,
	}, nil
}

func (s *Step) Name() string {
	return s.name
}

// должны где-то отправлять сообщение дальше, для саги
// при этом надо разграничить класс,
// который держит сагу и который держит пабсаб и остальные зависимости
// надо указать, а в какой-топик отправлять, а что делать
// ладно на самом деле тут можно сделать какую-то логику ретрая
// можно например добавить в структуру step поле onRetry и т.д.
// поэтому какой-то смысл есть наверное
func (s *Step) GetRouting() RoutingConfig {
	return s.routing
}

// Execute вызывает пользовательский хэндлер для выполнения шага.
func (s *Step) Execute(ctx context.Context, tx databases.TxQueryer, msg message.Message) (message.Message, error) {
	return s.execute(ctx, tx, msg)
}

// OnFail вызывает компенсирующее действие.
func (s *Step) OnFail(ctx context.Context, tx databases.TxQueryer, msg message.Message) (message.Message, error) {
	return s.compensate(ctx, tx, msg)
}

func (s *Step) GetOnError() ErrorHandler {
	return s.onError
}

func (s *Step) GetOnCompensateError() ErrorHandler {
	return s.onCompensateError
}

func (s *Step) GetRetryPolicy() *retrier.Retrier {
	return s.retryPolicy
}

// при получении сообщения, должны посмотреть на тип сообщения
// если тип execute - идем дальше
// если тип compensate - вызываем функцию компенсирования
// а если хотим retry действие а не компенсацию?
