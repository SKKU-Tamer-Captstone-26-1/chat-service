package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	var (
		dsn           = flag.String("dsn", os.Getenv("CHAT_DB_DSN"), "postgres DSN")
		migrationPath = flag.String("file", "migrations/001_create_chat_schema.sql", "migration SQL file path")
	)
	flag.Parse()

	if *dsn == "" {
		log.Fatal("dsn is required: pass -dsn or set CHAT_DB_DSN")
	}

	content, err := os.ReadFile(filepath.Clean(*migrationPath))
	if err != nil {
		log.Fatalf("read migration file failed: %v", err)
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

	if _, err := db.ExecContext(ctx, string(content)); err != nil {
		log.Fatalf("migration apply failed: %v", err)
	}

	log.Printf("migration applied successfully: %s", *migrationPath)
}
