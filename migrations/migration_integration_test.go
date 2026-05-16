package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/ontheblock/chat-service/internal/id"
)

func TestMigrationAppliesAndCreatesKeyIndexes(t *testing.T) {
	dsn := os.Getenv("CHAT_SERVICE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAT_SERVICE_TEST_PG_DSN to run postgres migration integration test")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx failed: %v", err)
	}
	defer tx.Rollback()

	schema := "chat_mig_" + strings.ReplaceAll(strings.ToLower(id.New()[:12]), "-", "_")
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatalf("create schema failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL search_path TO %s`, schema)); err != nil {
		t.Fatalf("set search_path failed: %v", err)
	}

	migrationSQL, err := os.ReadFile(filepath.Join("001_create_chat_schema.sql"))
	if err != nil {
		t.Fatalf("read migration failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, string(migrationSQL)); err != nil {
		t.Fatalf("apply migration failed: %v", err)
	}

	var idxCount int
	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_indexes
WHERE schemaname = $1 AND indexname = 'chat_rooms_board_active_unique_idx'
`, schema).Scan(&idxCount); err != nil {
		t.Fatalf("query index failed: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected board active unique index to exist")
	}

	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_indexes
WHERE schemaname = $1 AND indexname = 'chat_messages_room_sequence_unique_idx'
`, schema).Scan(&idxCount); err != nil {
		t.Fatalf("query index failed: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected room sequence unique index to exist")
	}

	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_tables
WHERE schemaname = $1 AND tablename = 'chat_device_tokens'
`, schema).Scan(&idxCount); err != nil {
		t.Fatalf("query device token table failed: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected chat_device_tokens table to exist")
	}

	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_indexes
WHERE schemaname = $1 AND indexname = 'chat_device_tokens_user_active_idx'
`, schema).Scan(&idxCount); err != nil {
		t.Fatalf("query device token index failed: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("expected device token active index to exist")
	}

	var enumCount int
	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_type t
JOIN pg_enum e ON e.enumtypid = t.oid
JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE n.nspname = $1 AND t.typname = 'message_type' AND e.enumlabel = 'FILE'
`, schema).Scan(&enumCount); err != nil {
		t.Fatalf("query enum failed: %v", err)
	}
	if enumCount != 1 {
		t.Fatalf("expected FILE enum label to exist in base schema")
	}
}

func TestIncrementalMigrationAddsFileMessageType(t *testing.T) {
	dsn := os.Getenv("CHAT_SERVICE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAT_SERVICE_TEST_PG_DSN to run postgres migration integration test")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx failed: %v", err)
	}
	defer tx.Rollback()

	schema := "chat_mig_" + strings.ReplaceAll(strings.ToLower(id.New()[:12]), "-", "_")
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatalf("create schema failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL search_path TO %s`, schema)); err != nil {
		t.Fatalf("set search_path failed: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
CREATE TYPE message_type AS ENUM ('TEXT', 'SYSTEM', 'IMAGE')
`); err != nil {
		t.Fatalf("create legacy enum failed: %v", err)
	}

	migrationSQL, err := os.ReadFile(filepath.Join("002_add_file_message_type.sql"))
	if err != nil {
		t.Fatalf("read migration failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, string(migrationSQL)); err != nil {
		t.Fatalf("apply incremental migration failed: %v", err)
	}

	var enumCount int
	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_type t
JOIN pg_enum e ON e.enumtypid = t.oid
JOIN pg_namespace n ON n.oid = t.typnamespace
WHERE n.nspname = $1 AND t.typname = 'message_type' AND e.enumlabel = 'FILE'
`, schema).Scan(&enumCount); err != nil {
		t.Fatalf("query enum failed: %v", err)
	}
	if enumCount != 1 {
		t.Fatalf("expected FILE enum label to be added by incremental migration")
	}
}

func TestIncrementalMigrationCreatesDeviceTokens(t *testing.T) {
	dsn := os.Getenv("CHAT_SERVICE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAT_SERVICE_TEST_PG_DSN to run postgres migration integration test")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx failed: %v", err)
	}
	defer tx.Rollback()

	schema := "chat_mig_" + strings.ReplaceAll(strings.ToLower(id.New()[:12]), "-", "_")
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`CREATE SCHEMA %s`, schema)); err != nil {
		t.Fatalf("create schema failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL search_path TO %s`, schema)); err != nil {
		t.Fatalf("set search_path failed: %v", err)
	}

	migrationSQL, err := os.ReadFile(filepath.Join("003_create_chat_device_tokens.sql"))
	if err != nil {
		t.Fatalf("read migration failed: %v", err)
	}
	if _, err := tx.ExecContext(ctx, string(migrationSQL)); err != nil {
		t.Fatalf("apply device token migration failed: %v", err)
	}

	var count int
	if err := tx.QueryRowContext(ctx, `
SELECT count(1)
FROM pg_tables
WHERE schemaname = $1 AND tablename = 'chat_device_tokens'
`, schema).Scan(&count); err != nil {
		t.Fatalf("query device token table failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected chat_device_tokens table to be created")
	}
}
