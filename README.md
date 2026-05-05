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

`make migrate` applies unapplied `.sql` files in `migrations/` in filename order.
For an existing database created before `FILE` message support, it will apply the enum upgrade without replaying the base schema.

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

## Attachment Uploads

Image and file attachments are optional and use signed GCS upload URLs.

- Configure `GCP_STORAGE_BUCKET`
- Authenticate with ADC, for example `gcloud auth application-default login`
- If ADC cannot auto-detect a signing identity, also set `GCP_SIGNING_SERVICE_ACCOUNT_EMAIL`

Current attachment flow:

1. Client calls `CreateAttachmentUploadURL` or `CreateImageUploadURL`
2. Request must include `user_id`, `room_id`, `file_name`, and `content_type`
3. Backend verifies the user is an active member of that room
4. Backend returns a room-scoped signed `PUT` upload URL
5. Client uploads raw bytes directly to GCS
6. Client calls `SendMessage` with:
   - `IMAGE` + `image_url`
   - `FILE` + `file_url` and file metadata

Current read flow:

- the bucket stays private
- chat-service stores internal object references
- `SendMessage`, `GetMessages`, and `StreamMessages` return signed read URLs for attachment messages
- signed read URL expiry is controlled by `GCS_READ_URL_EXPIRES_MINUTES` and defaults to `30`

Attachment URL validation is strict:

- upload URLs are bound to the target room
- `SendMessage` accepts only backend-issued URLs under the configured bucket and room prefix
- arbitrary external attachment URLs are rejected

Current GCS object layout is:

- `chat-attachments/<room_id>/<generated-id>.<ext>`

Note:

- upload URLs and read URLs are separate
- signed URLs should not be logged in full

## Migration Integration Test

This test is env-gated and runs only when DSN is provided.

```bash
CHAT_SERVICE_TEST_PG_DSN="$CHAT_DB_DSN" GOCACHE=/tmp/go-build-cache go test ./migrations -run TestMigrationAppliesAndCreatesKeyIndexes -v
```
