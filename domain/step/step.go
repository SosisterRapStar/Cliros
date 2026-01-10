package step

import (
	"context"
	"fmt"
	"maps"

	"github.com/SosisterRapStar/LETI-paper/domain/message"
	"github.com/google/uuid"
)

const (
	stepID = "stepID"
	sagaID = "sagaID"
)

type Execute func(ctx context.Context, msg message.Message) (message.Message, error)
type Compensate func(ctx context.Context, msg message.Message) (message.Message, error)

type StepParams struct {
	Name       string
	Context    map[string]string
	Execute    Execute
	Compensate Compensate
	Routing    RoutingConfig
}

type Step struct {
	name        string
	stepID      string
	sagaID      string
	context     map[string]string
	execute     Execute
	compensate  Compensate
	nextExecute string
	routing     RoutingConfig
}

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

func (a Step) Name() string {
	return a.name
}

func (a Step) StepID() string {
	return a.stepID
}

func (a Step) SagaID() string {
	return a.sagaID
}

func (a Step) Context() map[string]string {
	return maps.Clone(a.context)
}

// должны где-то отправлять сообщение дальше, для саги
// при этом надо разграничить класс,
// который держит сагу и который держит пабсаб и остальные зависимости
// надо указать, а в какой-топик отправлять, а что делать
func (a Step) Execute(ctx context.Context, msg message.Message) error {
	return nil
}

func (a Step) Compensate(ctx context.Context, msg message.Message) error {
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
