package migrations

var (
	PostgresOutboxMigration = `
	CREATE SCHEMA IF NOT EXISTS saga;

	CREATE TABLE IF NOT EXISTS saga.outbox (
		saga_id 		UUID NOT NULL,
		step_name 		TEXT NOT NULL,
		topic 			TEXT NOT NULL, 
		created_at 		TIMESTAMP NOT NULL,
		scheduled_at 	TIMESTAMP,
		metadata 		BYTEA,
		payload 		BYTEA NOT NULL,
		processed_at 	TIMESTAMP,
		
		PRIMARY KEY (saga_id, step_name)
	);

	CREATE INDEX IF NOT EXISTS idx_saga_outbox_topic ON saga.outbox (topic);
	CREATE INDEX IF NOT EXISTS idx_saga_outbox_created_at ON saga.outbox (created_at);
	CREATE INDEX IF NOT EXISTS idx_saga_outbox_scheduled_at ON saga.outbox (scheduled_at);
	`
)
