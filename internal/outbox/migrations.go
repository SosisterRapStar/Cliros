package outbox

// NOTE (временное решение): PRIMARY KEY установлен на (saga_id, topic).
// Так как один шаг агрегирует в себе execute- и compensate-части, раньше PK
// (saga_id, step_name) не позволял хранить больше одного сообщения от шага
// в рамках саги. Переносить уникальность на topic — минимально инвазивный
// компромисс, не требующий менять payload/meta.
//
// TODO: в идеале каждому исходящему сообщению нужно присваивать event_id
// (например, hash(saga_id + from_step + monotonic_seq или аналогичный
// детерминированный ключ идемпотентности) и делать PK именно по event_id.
// Это снимет любые коллизии при нескольких событиях от одного шага в один
// топик и даст честную идемпотентность на стороне broker-consumer'ов.
var (
	PostgresOutboxMigration = `
	CREATE TABLE IF NOT EXISTS public.outbox (
		saga_id 		UUID NOT NULL,
		step_name 		TEXT NOT NULL,
		topic 			TEXT NOT NULL, 
		created_at 		TIMESTAMP NOT NULL,
		scheduled_at 	TIMESTAMP NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
		metadata 		BYTEA,
		payload 		BYTEA NOT NULL,
		attempts_counter INTEGER DEFAULT 0,
		last_attempt    TIMESTAMP,
		processed_at 	TIMESTAMP,
		
		PRIMARY KEY (saga_id, topic)
	);

	CREATE INDEX IF NOT EXISTS idx_saga_outbox_step_name ON public.outbox (step_name);
	CREATE INDEX IF NOT EXISTS idx_saga_outbox_created_at ON public.outbox (created_at);
	CREATE INDEX IF NOT EXISTS idx_saga_outbox_scheduled_at ON public.outbox (scheduled_at);
	`

	MySQLOutboxMigration = `
	CREATE TABLE IF NOT EXISTS outbox (
		saga_id             CHAR(36) NOT NULL,
		step_name           VARCHAR(255) NOT NULL,
		topic               VARCHAR(255) NOT NULL,
		created_at          DATETIME NOT NULL,
		scheduled_at        DATETIME,
		metadata            LONGBLOB,
		payload             LONGBLOB NOT NULL,
		attempts_counter    INT DEFAULT 0,
		last_attempt        DATETIME,
		processed_at        DATETIME,
		PRIMARY KEY (saga_id, topic),
		INDEX idx_outbox_step_name (step_name),
		INDEX idx_outbox_created_at (created_at),
		INDEX idx_outbox_scheduled_at (scheduled_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`

	PostgresInboxMigration = `
	CREATE TABLE IF NOT EXISTS public.inbox (
		saga_id   UUID NOT NULL,
		from_step TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT (CURRENT_TIMESTAMP AT TIME ZONE 'UTC'),
		PRIMARY KEY (saga_id, from_step)
	);
	`

	MySQLInboxMigration = `
	CREATE TABLE IF NOT EXISTS inbox (
		saga_id   CHAR(36) NOT NULL,
		from_step VARCHAR(255) NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (saga_id, from_step)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
)
