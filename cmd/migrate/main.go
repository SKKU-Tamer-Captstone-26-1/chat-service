package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const baseMigrationFile = "001_create_chat_schema.sql"

func main() {
	var (
		dsn           = flag.String("dsn", os.Getenv("CHAT_DB_DSN"), "postgres DSN")
		migrationPath = flag.String("path", "migrations", "migration SQL file path or directory")
		legacyFile    = flag.String("file", "", "deprecated alias for -path")
	)
	flag.Parse()
	if *legacyFile != "" {
		*migrationPath = *legacyFile
	}

	if *dsn == "" {
		log.Fatal("dsn is required: pass -dsn or set CHAT_DB_DSN")
	}

	files, err := resolveMigrationFiles(*migrationPath)
	if err != nil {
		log.Fatalf("resolve migration files failed: %v", err)
	}

	db, err := sql.Open("pgx", *dsn)
	if err != nil {
		log.Fatalf("open db failed: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("db ping failed: %v", err)
	}

	if err := ensureMigrationTable(ctx, db); err != nil {
		log.Fatalf("ensure migration table failed: %v", err)
	}

	applied, err := loadAppliedMigrations(ctx, db)
	if err != nil {
		log.Fatalf("load applied migrations failed: %v", err)
	}

	if _, ok := applied[baseMigrationFile]; !ok {
		exists, err := baseSchemaExists(ctx, db)
		if err != nil {
			log.Fatalf("check base schema failed: %v", err)
		}
		if exists {
			if err := markMigrationApplied(ctx, db, baseMigrationFile); err != nil {
				log.Fatalf("bootstrap base migration failed: %v", err)
			}
			applied[baseMigrationFile] = struct{}{}
			log.Printf("base schema already exists, marked %s as applied", baseMigrationFile)
		}
	}

	for _, file := range files {
		name := filepath.Base(file)
		if _, ok := applied[name]; ok {
			log.Printf("migration already applied, skipping: %s", name)
			continue
		}

		content, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			log.Fatalf("read migration file failed (%s): %v", file, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Fatalf("begin migration tx failed (%s): %v", file, err)
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			log.Fatalf("migration apply failed (%s): %v", file, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (filename, applied_at)
VALUES ($1, now())
ON CONFLICT (filename) DO NOTHING
`, name); err != nil {
			_ = tx.Rollback()
			log.Fatalf("record migration failed (%s): %v", file, err)
		}
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit migration failed (%s): %v", file, err)
		}
		log.Printf("migration applied successfully: %s", file)
	}
}

func resolveMigrationFiles(path string) ([]string, error) {
	cleanPath := filepath.Clean(path)
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []string{cleanPath}, nil
	}

	entries, err := os.ReadDir(cleanPath)
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(strings.ToLower(name), ".sql") {
			files = append(files, filepath.Join(cleanPath, name))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return nil, fmt.Errorf("no sql files found under %s", cleanPath)
	}
	return files, nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  filename text PRIMARY KEY,
  applied_at timestamptz NOT NULL DEFAULT now()
)
`)
	return err
}

func loadAppliedMigrations(ctx context.Context, db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.QueryContext(ctx, `SELECT filename FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]struct{})
	for rows.Next() {
		var filename string
		if err := rows.Scan(&filename); err != nil {
			return nil, err
		}
		out[filename] = struct{}{}
	}
	return out, rows.Err()
}

func baseSchemaExists(ctx context.Context, db *sql.DB) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM information_schema.tables
  WHERE table_schema = current_schema()
    AND table_name = 'chat_rooms'
)
`).Scan(&exists)
	return exists, err
}

func markMigrationApplied(ctx context.Context, db *sql.DB, filename string) error {
	_, err := db.ExecContext(ctx, `
INSERT INTO schema_migrations (filename, applied_at)
VALUES ($1, now())
ON CONFLICT (filename) DO NOTHING
`, filename)
	return err
}
