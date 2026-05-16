# chat-service
Group-chat-only backend service for On the Block.

![ERD](/docs/images/chat-database-erd.png)

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

## Auth Modes

- `CHAT_AUTH_MODE=dev` (default): pre-auth/local mode. The service trusts request-body user IDs such as `user_id`, `creator_user_id`, and `sender_user_id`.
- `CHAT_AUTH_MODE=validate_token`: production mode. Every gRPC call must include `Authorization: Bearer <access_token>`.

In `validate_token` mode, chat-service calls auth-service `ValidateToken`, derives the internal user ID from `ValidateTokenResponse.user_id`, then calls `GetMe` for the authenticated user's own profile including `profile_image_url`. Chat-service still persists only chat-domain IDs such as `sender_user_id`; profile and avatar data remain owned by auth-service.

```bash
CHAT_AUTH_MODE=validate_token
CHAT_AUTH_SERVICE_URL=https://authorization-service-44649239380.asia-northeast3.run.app
```

For local auth-service:

```bash
CHAT_AUTH_MODE=validate_token
CHAT_AUTH_GRPC_ADDR=localhost:9090
CHAT_AUTH_INSECURE=true
```

If the Cloud Run auth service is private, the chat-service runtime identity must be allowed to invoke it.

## Board-context Chat Entry

Use `GetOrCreateBoardChatRoom` when the client enters chat from a Board post.
Chat-service does not load nearby Board posts or store Board content. It only receives
`board_id`, opens the active linked room, or creates one if none exists.

The authenticated user comes from auth metadata, not a request-body `user_id`.
`board_owner_user_id` is optional and is added as a room member when provided, but it
must be validated against Board-service before it is used for ownership or stronger
authorization decisions.

## Chat Read State

Room-member read state uses `last_read_sequence_no`.
`ListMyRooms` returns `unread_count` per room, excluding messages sent by the same user.
Clients can sum room `unread_count` values for the Chat tab badge.
Use `MarkChatRoomRead` when the authenticated user opens a room; the server marks that
member read through the latest message sequence.
The older `MarkAsRead` RPC remains available for sequence-specific compatibility.

## Chat Push Notifications

Chat push is chat-message-only. It is separate from Board/notice notifications and the top bell notification.

Device tokens are owned by the authenticated user:

- `RegisterDeviceToken(device_id, token, platform)`
- `UnregisterDeviceToken(device_id)`

When `SendMessage` creates a new message, chat-service looks up active room members except the sender, finds their active FCM tokens, and sends a push payload:

- `type=chat_message`
- `room_id=<room_id>`
- `message_id=<message_id>`

FCM dispatch is disabled unless configured:

```bash
CHAT_PUSH_FCM_ENABLED=true
CHAT_FCM_PROJECT_ID=your-firebase-project-id
```

The runtime needs ADC or service-account credentials with Firebase Messaging permission. Message persistence and unread counts remain the source of truth; push delivery failure does not fail `SendMessage`.

Flutter/client integration checklist:

1. Request notification permission after login.
2. Get the FCM registration token and a stable device/install ID.
3. Call `RegisterDeviceToken` with `platform=IOS` or `ANDROID`.
4. On logout or token rotation, call `UnregisterDeviceToken` or re-register the new token.
5. For foreground chat pushes, refresh room unread counts or apply the streamed message state.
6. For opened/background pushes, read `room_id` from the data payload and navigate to that chat room.

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

The canonical bucket env var in this repository is `GCP_STORAGE_BUCKET`.
Do not use `GCS_BUCKET_NAME` unless you also change the application code.

Current attachment flow:

1. Client calls `CreateAttachmentUploadURL` or `CreateImageUploadURL`
2. Request must include `user_id`, `room_id`, `file_name`, and `content_type`
3. Backend verifies the user is an active member of that room
4. Backend returns a room-scoped signed `PUT` upload URL
5. Client uploads raw bytes directly to GCS
6. Client calls `SendMessage` with:
   - `IMAGE` + `image_url`
   - `FILE` + `file_url` and file metadata

Upload URL expiry:

- currently fixed at `15` minutes in code
- `GCS_UPLOAD_URL_EXPIRES_MINUTES` is not implemented yet

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
