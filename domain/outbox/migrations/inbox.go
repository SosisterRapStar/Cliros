package migrations

var (
	PostgresInboxMigration = `
	CREATE SCHEMA IF NOT EXISTS saga;

	CREATE TABLE IF NOT EXISTS saga.inbox (
		saga_id   UUID NOT NULL,
		from_step TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT NOW(),
		PRIMARY KEY (saga_id, from_step)
	);
	`
	MySQLInboxMigration = `
	CREATE DATABASE IF NOT EXISTS saga;
	USE saga;

	CREATE TABLE IF NOT EXISTS inbox (
		saga_id   CHAR(36) NOT NULL,
		from_step VARCHAR(255) NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (saga_id, from_step)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
	`
)
