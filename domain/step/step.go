package step

import (
	"context"
	"fmt"
	"maps"

	"github.com/SosisterRapStar/LETI-paper/domain/broker"
	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/google/uuid"
)

// нам нужна точка входа для создание саги
// какая-то такая хуета при которой мы понимаем, что создали сагу и вот id ей присвоили
// может быть и такое, что мы не создали сагу, а являемся одним из сервисов, который должен выполнить транзакцию
// поэтому id саги получим оттуда
// нужно дать пользователю возможность самому формировать id саги
const (
	stepID = "stepID"
	sagaID = "sagaID"
)

type Execute func(ctx context.Context, msg message.Message) (message.Message, error)
type Compensate func(ctx context.Context, msg message.Message) (message.Message, error)
type IDFunc func() string

type StepParams struct {
	Name        string
	Context     map[string]string
	Execute     Execute
	Compensate  Compensate
	Routing     RoutingConfig
	IDGenerator IDFunc
}

type Step struct {
	name        string
	stepID      string // какая-то залупа, это должно быть не тут
	sagaID      string // какая-то залупа, id саги должны брать из пришедших messages
	context     map[string]string
	execute     Execute
	compensate  Compensate
	nextExecute string
	routing     RoutingConfig
	idGenerator IDFunc
}

// проблема в том, что мы можем сделать компенсацию как для нашего шага, так сделать onCompensate для шага execute

func New(p StepParams) (Step, error) {
	if p.Name == "" {
		return Step{}, fmt.Errorf("Step name is required")
	}

	if p.Context == nil {
		p.Context = make(map[string]string)
	}

	defaultMeta, err := getDefaultMeta()
	if err != nil {
		return Step{}, err
	}

	if p.IDGenerator == nil {
		p.IDGenerator = newID
	}

	// Используем значения по умолчанию только если пользователь не указал свои
	for k, v := range defaultMeta {
		if _, exists := p.Context[k]; !exists {
			p.Context[k] = v
		}
	}

	return Step{
		name:       p.Name,
		stepID:     defaultMeta[stepID],
		sagaID:     defaultMeta[sagaID],
		context:    p.Context,
		execute:    p.Execute,
		compensate: p.Compensate,
		routing:    p.Routing,
	}, nil
}

func (s Step) Name() string {
	return s.name
}

func (s Step) StepID() string {
	return s.stepID
}

func (s Step) SagaID() string {
	return s.sagaID
}

func (s Step) Context() map[string]string {
	return maps.Clone(s.context)
}

// должны где-то отправлять сообщение дальше, для саги
// при этом надо разграничить класс,
// который держит сагу и который держит пабсаб и остальные зависимости
// надо указать, а в какой-топик отправлять, а что делать
func (s Step) Execute(ctx context.Context, msg message.Message, pubsub broker.Pubsub) error {
	newMessage, err := s.execute(ctx, msg)
	if err != nil {
		// так как произошла ошибка, можем либо повторить, либо делать сразу компенсацию
		// для повтора сделаем декоратор с повторами, но потом
		// для компенсаций нужен отдельный message
		// нужно как-то сделать маппер сообщений как будто, если пользователь хочет передать какую-то
		s.compensate(ctx)
	}
}

func (s Step) Compensate(ctx context.Context, msg message.Message) error {
	return nil
}

func newID() (uuid.UUID, error) {
	return uuid.NewV7()
}

func getDefaultMeta() (map[string]string, error) {
	stID, err := newID()
	if err != nil {
		return nil, err
	}

	sID, err := newID()
	if err != nil {
		return nil, err
	}

	return map[string]string{
		stepID: stID.String(),
		sagaID: sID.String(),
	}, nil
}

// при получении сообщения, должны посмотреть на тип сообщения
// если тип execute - идем дальше
// если тип compensate - вызываем функцию компенсирования
// а если хотим retry действие а не компенсацию?
