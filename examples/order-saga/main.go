package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/SosisterRapStar/LETI-paper/backoff"
	"github.com/SosisterRapStar/LETI-paper/controller"
	"github.com/SosisterRapStar/LETI-paper/database"
	"github.com/SosisterRapStar/LETI-paper/message"
	"github.com/SosisterRapStar/LETI-paper/retry"
	"github.com/SosisterRapStar/LETI-paper/step"
)

type stubPubsub struct{}

func (s *stubPubsub) Subscribe(_ context.Context, topic string, _ func(context.Context, message.Message) error) error {
	log.Printf("[stub] subscribed to topic: %s", topic)
	return nil
}
func (s *stubPubsub) Close() error { return nil }

func (s *stubPubsub) Publish(_ context.Context, topic string, msg message.Message) error {
	log.Printf("[stub] published to %s: sagaID=%s, from=%s", topic, msg.SagaID, msg.FromStep)
	return nil
}

func main() {
	ctx := context.Background()

	// 1. Подключение к PostgreSQL через стандартный database/sql
	db, err := sql.Open("pgx", "postgres://postgres:postgres@localhost:5432/saga_example?sslmode=disable")
	if err != nil {
		log.Fatalf("failed to connect to postgres: %v", err)
	}
	defer db.Close()

	// 2. Создаём DBContext — абстракция над БД, не зависит от конкретного драйвера
	dbCtx := database.NewDBContext(db, database.SQLDialectPostgres)

	// 3. Канал для ошибок Reader'а
	errCh := make(chan error, 128) //nolint:mnd

	// 4. Создаём Controller — вся внутренняя обвязка (outbox reader/writer, inbox, executor) создаётся автоматически
	pub := &stubPubsub{}
	ctrl, err := controller.New(&controller.Config{
		Subscriber: pub,
		Publisher:  pub,
		DB:         dbCtx,

		InfraRetry: &retry.Retrier{
			BackoffOptions: retry.BackoffOptions{
				BackoffPolicy: backoff.Expontential{},
				MinBackoff:    50 * time.Millisecond, //nolint:mnd
				MaxBackoff:    5 * time.Second,       //nolint:mnd
			},
			MaxRetries: 10, //nolint:mnd
		},

		PollInterval:  1 * time.Second,
		BatchSize:     10, //nolint:mnd
		BackoffPolicy: backoff.Expontential{},
		BackoffMin:    100 * time.Millisecond, //nolint:mnd
		BackoffMax:    1 * time.Minute,
		ErrCh:         errCh,
	})
	if err != nil {
		log.Fatalf("failed to create controller: %v", err)
	}

	// 5. Определяем шаг саги "create-order"
	//
	// Execute: пользовательская бизнес-логика выполняется в той же транзакции,
	//          что и запись в outbox. Всё атомарно — либо и заказ создан,
	//          и сообщение записано, либо ничего.
	//
	// Compensate: отмена заказа при получении failure-сообщения от другого сервиса.
	orderStep, err := step.New(&step.StepParams{
		Name: "create-order",
		Execute: func(ctx context.Context, tx database.TxQueryer, msg message.Message) (message.Message, error) {
			orderID := "order-123"
			userID := "user-456"
			amount := 99.99

			// Бизнес-логика: INSERT в таблицу orders — в той же TX, что и outbox
			_, err := tx.ExecContext(ctx,
				"INSERT INTO orders (id, user_id, amount, status) VALUES ($1, $2, $3, $4)",
				orderID, userID, amount, "created",
			)
			if err != nil {
				return message.Message{}, fmt.Errorf("insert order: %w", err)
			}

			log.Printf("order created: %s for user %s, amount %.2f", orderID, userID, amount)

			// Формируем сообщение для следующего шага
			return message.Message{
				MessagePayload: message.MessagePayload{
					Payload: map[string]any{
						"order_id": orderID,
						"user_id":  userID,
						"amount":   amount,
					},
				},
			}, nil
		},
		Compensate: func(ctx context.Context, tx database.TxQueryer, msg message.Message) (message.Message, error) {
			orderID := msg.Payload["order_id"]

			// Компенсация: отмена заказа — в той же TX, что и outbox
			_, err := tx.ExecContext(ctx,
				"UPDATE orders SET status = $1 WHERE id = $2",
				"cancelled", orderID,
			)
			if err != nil {
				return message.Message{}, fmt.Errorf("cancel order: %w", err)
			}

			log.Printf("order cancelled: %v", orderID)
			return msg, nil
		},
		Routing: step.RoutingConfig{
			// После успешного создания заказа — отправить сообщение в payment-сервис
			NextStepTopics: []string{"payment-service.process"},
			// При ошибке — уведомить сервис, который запустил сагу
			ErrorTopics: []string{"order-service.compensate"},
		},
		// Пример: обработчик ошибок. Если Execute упал — можно решить, что делать
		OnError: func(_ context.Context, _ database.TxQueryer, msg message.Message, err error) (message.Message, error) {
			log.Printf("execute error handler: %v", err)
			// Возвращаем ошибку — значит отправим failure event
			return msg, err
		},
	})
	if err != nil {
		log.Fatalf("failed to create step: %v", err)
	}

	// 6. Регистрируем шаг на топик
	if err := ctrl.Register("order-service.create", orderStep); err != nil {
		log.Fatalf("failed to register step: %v", err)
	}

	// 7. Инициализация: миграции + запуск Reader (фоновый поллинг outbox -> publish)
	if err := ctrl.Init(ctx); err != nil {
		log.Fatalf("failed to init controller: %v", err)
	}
	defer ctrl.Close()

	// 8. Запуск обработки ошибок Reader'а в фоне
	go func() {
		for err := range errCh {
			log.Printf("[reader error] %v", err)
		}
	}()

	// 9. Стартуем сагу — Executor создаёт TX, вызывает Execute, пишет outbox, коммитит
	initialMsg := message.Message{}
	if err := ctrl.StartSaga(ctx, orderStep, initialMsg); err != nil {
		log.Fatalf("failed to start saga: %v", err)
	}

	log.Println("saga started successfully, outbox messages will be published by the Reader")
}
