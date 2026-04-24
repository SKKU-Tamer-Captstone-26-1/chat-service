.PHONY: up down logs migrate run run-memory smoke test proto

ifneq (,$(wildcard .env))
include .env
export
endif

up:
	docker compose up -d chat-postgres

down:
	docker compose down

logs:
	docker compose logs -f chat-postgres

migrate:
	go run ./cmd/migrate -dsn "$(CHAT_DB_DSN)"

run:
	go run ./cmd/chat-service

run-memory:
	CHAT_REPOSITORY=memory go run ./cmd/chat-service

smoke:
	go run ./cmd/smoke

test:
	GOCACHE=/tmp/go-build-cache go test ./...

proto:
	PATH=/tmp/bin:$$PATH protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/chat/v1/chat.proto
