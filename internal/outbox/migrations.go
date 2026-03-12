package outbox

var (
	PostgresOutboxMigration = `
	CREATE SCHEMA IF NOT EXISTS saga;

	CREATE TABLE IF NOT EXISTS saga.outbox (
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
		
		PRIMARY KEY (saga_id, step_name)
	);

	CREATE INDEX IF NOT EXISTS idx_saga_outbox_topic ON saga.outbox (topic);
	CREATE INDEX IF NOT EXISTS idx_saga_outbox_created_at ON saga.outbox (created_at);
	CREATE INDEX IF NOT EXISTS idx_saga_outbox_scheduled_at ON saga.outbox (scheduled_at);
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
		PRIMARY KEY (saga_id, step_name),
		INDEX idx_outbox_topic (topic),
		INDEX idx_outbox_created_at (created_at),
		INDEX idx_outbox_scheduled_at (scheduled_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`

	PostgresInboxMigration = `
	CREATE SCHEMA IF NOT EXISTS saga;

	CREATE TABLE IF NOT EXISTS saga.inbox (
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
		created_at DATETIME NOT NULL DEFAULT UTC_TIMESTAMP(),
		PRIMARY KEY (saga_id, from_step)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
)
