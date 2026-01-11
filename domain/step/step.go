package step

import (
	"context"
	"fmt"

	"github.com/SosisterRapStar/LETI-paper/domain/message"
)

// нам нужна точка входа для создание саги
// какая-то такая хуета при которой мы понимаем, что создали сагу и вот id ей присвоили
// может быть и такое, что мы не создали сагу, а являемся одним из сервисов, который должен выполнить транзакцию
// поэтому id саги получим оттуда
// нужно дать пользователю возможность самому формировать id саги

// желательно отслеживать id шага или дать шагу имя
// const (
// 	name   = "name"
// 	sagaID = "sagaID"
// )

type Execute func(ctx context.Context, msg message.Message) (message.Message, error)
type Compensate func(ctx context.Context, msg message.Message) (message.Message, error)

// здесь можно указать логику, что делать при ошибке, если нужно например сделать retry действия, а не сразу откатить транзакцию
type ErrorHandler func(ctx context.Context, msg message.Message, err error) (message.Message, error)

type IDFunc func() string

// обязательно подумать о том, что нам нужно хранить локальные метаданные для каждой саги и что это очень обязательно
// то есть каждый step, должен хранить о себе данные
// пусть будем хранить данные так: saga_id + step_id
type StepParams struct {
	Name       string
	Execute    Execute
	Compensate Compensate
	Routing    RoutingConfig
	// RetryPolicy *RetryPolicy
	OnError ErrorHandler
}

type Step struct {
	name string
	// sagaID      string
	// какая-то залупа, id саги должны брать из пришедших messages
	execute    Execute
	compensate Compensate
	routing    RoutingConfig
	// пользователь должен сформировать правильное сообщение для другого сервиса, дать ему контекст, чтобы тот откатился или что-то сделал
	onError ErrorHandler
	// retryPolicy *RetryPolicy
}

// проблема в том, что мы можем сделать компенсацию как для нашего шага, так сделать onCompensate для шага execute

func New(p StepParams) (Step, error) {
	if p.Name == "" {
		return Step{}, fmt.Errorf("Step name is required")
	}

	return Step{
		name:       p.Name,
		execute:    p.Execute,
		compensate: p.Compensate,
		routing:    p.Routing,
		onError:    p.OnError,
	}, nil
}

func (s Step) Name() string {
	return s.name
}

// должны где-то отправлять сообщение дальше, для саги
// при этом надо разграничить класс,
// который держит сагу и который держит пабсаб и остальные зависимости
// надо указать, а в какой-топик отправлять, а что делать
// ладно на самом деле тут можно сделать какую-то логику ретрая
// можно например добавить в структуру step поле onRetry и т.д.
// поэтому какой-то смысл есть наверное
func (s Step) GetRouting() RoutingConfig {
	return s.routing
}

func (s Step) Execute(ctx context.Context, msg message.Message) (message.Message, error) {
	return s.execute(ctx, msg)
}

// // для повтора сделаем декоратор с повторами, но потом

// не знаю зачем я сделал такую логику тупую, типа нахуя, можно же просто вызывать s.compensate
// но я не почему-то не могу так сделать, что-то внутри хочет сделать этот ебанный полугеттер полухуй

func (s Step) OnFail(ctx context.Context, msg message.Message) (message.Message, error) {
	return s.compensate(ctx, msg)
}

func (s Step) GetOnError() ErrorHandler {
	return s.onError
}

// при получении сообщения, должны посмотреть на тип сообщения
// если тип execute - идем дальше
// если тип compensate - вызываем функцию компенсирования
// а если хотим retry действие а не компенсацию?
