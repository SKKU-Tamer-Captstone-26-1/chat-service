# chat-service

Group-chat-only backend service for On the Block.

## Local PostgreSQL Development

### 1. Prepare env

```bash
cp .env.example .env
source .env
```

### 2. Start local PostgreSQL

```bash
make up
```

PostgreSQL runs at `localhost:55432`.

### 3. Apply migration

```bash
make migrate
```

### 4. Run chat-service

```bash
make run
```

Default runtime is PostgreSQL (`CHAT_REPOSITORY=postgres`).

### 5. Run smoke check

```bash
make smoke
```

`make smoke` runs:
- `CreateRoom`
- `JoinRoom`
- `SendMessage`
- `GetMessages`
- `MarkAsRead`

## Runtime Modes

- `CHAT_REPOSITORY=postgres` (default): requires `CHAT_DB_DSN`
- `CHAT_REPOSITORY=memory`: in-memory development mode

```bash
make run-memory
```

## Useful Commands

```bash
make test      # run all tests
make logs      # follow postgres logs
make down      # stop local postgres
make smoke     # grpc end-to-end smoke check
make proto     # regenerate protobuf Go stubs
```

## Migration Integration Test

This test is env-gated and runs only when DSN is provided.

```bash
CHAT_SERVICE_TEST_PG_DSN="$CHAT_DB_DSN" GOCACHE=/tmp/go-build-cache go test ./migrations -run TestMigrationAppliesAndCreatesKeyIndexes -v
```
