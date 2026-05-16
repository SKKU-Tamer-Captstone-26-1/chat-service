# Building a Safer v1 Chat Backend in Go, PostgreSQL, and gRPC

This post summarizes the backend work completed in the `chat-service` repository during this iteration. The service is a group-chat-only backend for On the Block, built with Go, PostgreSQL, and gRPC.

The focus of this session was not adding flashy new features. It was making the baseline chat flow correct under real usage:

- message persistence had to be safe under concurrent sends
- room list responses had to match the proto contract
- stream reconnect behavior had to stop dropping messages
- pagination had to survive rooms with no messages
- local multi-user testing had to become easier

Later in the same development cycle, the attachment path was also expanded and hardened:

- `FILE` message support was added alongside `IMAGE`
- PostgreSQL enum migration support was added for existing databases
- GCS signed upload URLs were added for attachments
- upload URL issuance was bound to room membership
- `SendMessage` started rejecting attachment URLs outside the trusted bucket and room prefix

The latest implementation also added the backend pieces needed for production chat entry, badges, and chat-message push:

- board-context chat entry through `GetOrCreateBoardChatRoom`
- real read/unread state through `unread_count` and `MarkChatRoomRead`
- chat-message push notification support through FCM device token registration and chat-only push dispatch

## Context

The repository scope stayed aligned with the project rules:

- group chat only
- room types limited to `GENERAL_GROUP` and `BOARD_LINKED_GROUP`
- message types limited to `TEXT`, `SYSTEM`, and `IMAGE`
- later expanded to include `FILE`
- PostgreSQL as the source of truth
- gRPC as the service contract
- no Redis dependency in v1
- production auth mode can derive user identity from auth-service token validation
- request-body `user_id` remains only for local/dev compatibility paths
- chat push is limited to chat messages and remains separate from Board/notice/top-bell notification features

The goal was to improve correctness without drifting outside those boundaries.

## 1. Hardening Per-Room Message Sequencing

The first issue was message ordering under concurrency.

Each room uses a monotonically increasing `sequence_no`, and the combination `(room_id, sequence_no)` must stay unique. A simple `MAX(sequence_no) + 1` approach works in single-user testing, but it is unsafe when multiple users send to the same room at nearly the same time.

### Problem

Two concurrent sends could:

1. read the same current max sequence
2. compute the same next sequence
3. race to insert

That creates duplicate-key errors or unstable ordering behavior.

### Fix

The PostgreSQL repository was changed to allocate the next sequence inside a transaction and guard it with a transaction-scoped advisory lock keyed by `room_id`.

That gave us:

- one writer at a time per room for sequence allocation
- no cross-room serialization
- stable room-local ordering

### Why this approach

This keeps PostgreSQL as the source of truth and avoids introducing Redis or a separate sequencing system before v1 actually needs it.

## 2. Adding PostgreSQL Repository Tests

Earlier tests covered service rules and gRPC behavior well, but the PostgreSQL repository layer needed stronger verification.

New repository tests were added to cover:

- concurrent `CreateMessageWithNextSequence`
- room list unread counts
- room list pagination
- last-message behavior in summaries
- room summaries when a room has no messages

This was important because several real bugs in this session were not “business rule” bugs. They were SQL/query/scan bugs, and those only show up reliably when the repository layer is exercised directly against PostgreSQL.

## 3. Completing `ListMyRooms.last_message`

The proto already exposed `last_message` in `ChatRoomSummary`, but the backend response did not populate it.

That meant the contract was ahead of the implementation.

### What changed

The room-summary domain model was extended to include `LastMessage`, and both memory and PostgreSQL repositories were updated to populate it.

On the PostgreSQL side, `ListRoomsByUser` now uses a `LEFT JOIN LATERAL` to fetch the latest message per room efficiently.

The gRPC layer maps that into `LastMessagePreview`, including:

- `message_id`
- `message_type`
- `content_preview`
- `sender_user_id`
- `sequence_no`
- `sent_at`

Deleted messages are still sanitized before being exposed.

## 4. Fixing a Real Pagination Bug in Room Lists

After wiring `last_message`, a real production-style bug surfaced.

### Symptom

Flutter could load page 1 of `ListMyRooms`, but page 2 failed with:

```text
sql: Scan error on column index 13, name "message_type": converting NULL to string is unsupported
```

### Root cause

Rooms without any messages produce `NULL` values from the `LEFT JOIN LATERAL`. The repository scan logic handled several nullable columns safely, but `message_type` was still scanned into a non-nullable Go type.

That meant:

- page 1 could work
- page 2 could crash if it included an older room with no messages
- older rooms would never show in the Flutter room list

### Fix

`message_type` was changed to scan through a nullable wrapper first, and `LastMessage` is now constructed only when a message row actually exists.

### Added regression test

A PostgreSQL repository test now explicitly verifies pagination across:

- one room with a last message
- one room without any messages

This kind of test matters because it protects a behavior that looks fine in small happy-path demos but breaks once mixed room states exist in real data.

## 5. Improving Stream Reconnect Correctness

Streaming was the hardest backend change in this session.

The original `StreamMessages` implementation was simple and workable for early testing, but it had a correctness gap:

- it replayed only a fixed slice of recent messages
- it subscribed after the replay path
- reconnects could miss messages in the handoff window

That is exactly the sort of bug that looks invisible until real users reconnect after backgrounding the app or losing the network for a short time.

### Design constraints

We stayed within the documented v1 boundaries:

- keep PostgreSQL as source of truth
- keep pub/sub as the real-time delivery mechanism
- do not switch to DB polling as the main model
- do not introduce Redis yet

### What changed

The stream path now:

1. validates active membership
2. subscribes to room pub/sub
3. catches up from PostgreSQL using forward queries after `after_sequence_no`
4. buffers messages by `sequence_no`
5. only flushes contiguous messages in order
6. backfills from the repository again if a gap is detected while live messages are arriving

To support that, a new repository method was added:

- `ListMessagesAfter`

This was implemented in both:

- the in-memory repository
- the PostgreSQL repository

### Why this matters

This makes reconnect/resume behavior safer in these cases:

- the client was disconnected and missed many messages
- more than one catch-up batch is needed
- a live message arrives before an earlier missed sequence is seen through the stream

The implementation is still v1-friendly:

- pub/sub remains simple and replaceable
- PostgreSQL remains authoritative
- no Redis dependency was introduced

## 6. Catching a Local Runtime Issue That Looked Like a Flutter Bug

Not every failure turned out to be a code bug.

At one point, Flutter could list rooms but sending a message failed. It looked like the Enter button was broken in the UI.

After tracing the backend directly, the actual issue was:

- an older `chat-service` process was still running on port `9090`
- Flutter was talking to that stale binary
- the stale binary still contained an older broken SQL path

This was a useful reminder:

- when transport works but one operation fails strangely, check the actual running server process before blaming the client

The smoke client and direct process inspection made this clear quickly.

## 7. Making Local Multi-User Chat Testing Easier

The smoke client was also extended.

### Before

It only performed a short end-to-end smoke check:

- create room
- join room
- send message
- get messages
- mark as read

### After

It can now:

- join two extra local test users in smoke mode
- run in interactive `chat` mode
- join an existing room as another member
- stream live messages in the terminal
- send terminal-entered messages to the room

This made it much easier to test:

- Flutter-to-terminal live chat
- multi-user streaming behavior
- reconnect scenarios

without needing another full frontend instance every time.

## 8. Adding First-Class File Attachments

The original v1 direction only covered `TEXT`, `SYSTEM`, and `IMAGE`, but the implementation later needed real document attachments as well.

### What changed

- `MESSAGE_TYPE_FILE` was added to the proto contract
- `file_url` was added to chat message payloads
- file metadata support was added for values such as:
  - `file_name`
  - `content_type`
- the PostgreSQL enum for `message_type` was upgraded to include `FILE`

### Migration work

This surfaced a real schema/versioning issue: application code could start sending `FILE` before the existing database enum allowed it.

That was fixed by:

- updating the base schema
- adding an idempotent incremental migration for older databases
- updating the migration runner to track applied files instead of replaying everything

## 9. Adding Signed GCS Upload URLs

The next step was moving attachments away from fake text placeholders and into a real upload flow.

### Current write flow

1. client requests a signed upload URL from chat-service
2. backend verifies the user and room
3. backend returns a signed GCS `PUT` URL
4. client uploads bytes directly to GCS
5. client sends a normal chat message with the backend-issued attachment URL

This keeps binary data out of PostgreSQL and keeps chat-service responsible for message metadata only.

## 10. Hardening the Attachment Security Boundary

The first version of signed uploads was functional but too permissive.

### Problems found

- upload URL issuance was not bound to room membership
- attachment URLs in `SendMessage` were too trusting
- clients could potentially reference untrusted external URLs

### Fixes applied

- upload URL RPCs now require `room_id`
- backend verifies the caller is an active member before signing
- object names are room-scoped under:
  - `chat-attachments/<room_id>/...`
- `SendMessage` now accepts only trusted bucket URLs under the correct room prefix

This does not finish the full production attachment model yet, but it closes the most immediate abuse gap in the current architecture.

## 11. Moving to Private-Bucket Signed Read URLs

The next production step after signed uploads was read access policy.

The backend now:

- keeps the GCS bucket private
- stores internal object references instead of treating public object URLs as the source of truth
- generates signed read URLs on message delivery for attachment messages

That read-URL mapping is applied to:

- `SendMessage`
- `GetMessages`
- `StreamMessages`

This let the Flutter client render and open private-bucket attachments without making the bucket public.

## 12. Adding Board-Context Chat Entry

The product rule for Board to Chat is now explicit:

- the client may show nearby Board previews
- chat-service must not fetch nearby Board posts
- chat-service receives only Board context and opens the related chat room
- the chat list should not create arbitrary random rooms as a shortcut

### What changed

A new gRPC API was added:

```text
GetOrCreateBoardChatRoom(board_id, title, board_owner_user_id)
```

The authenticated user is derived from auth metadata, not from a request-body `user_id`.

The service now:

1. checks for an active `BOARD_LINKED_GROUP` room for the given `board_id`
2. returns that room if it already exists
3. creates a new board-linked room only if none exists
4. adds the authenticated user as an active room member
5. optionally adds `board_owner_user_id` as a member when the client already has it

The existing PostgreSQL partial unique index still enforces one active room per board:

```text
room_type = BOARD_LINKED_GROUP
is_active = true
deleted_at IS NULL
```

### Boundary kept

The service does not store Board content.

`board_owner_user_id` is currently treated only as optional member data. It must be validated through Board-service before chat-service uses it for ownership or stronger authorization decisions.

## 13. Implementing Real Read/Unread State

The schema already had room-member read progress through:

```text
last_read_sequence_no
```

Because each room has stable monotonically increasing `sequence_no`, this remains the read cursor for v1.

### What changed

`ListMyRooms` now returns real per-room `unread_count`.

Unread messages are counted as:

```text
message.sequence_no > member.last_read_sequence_no
AND message.sender_user_id != member.user_id
```

That means:

- a sender does not get unread count from their own messages
- other active members see the message as unread until they mark the room read
- the client can sum room `unread_count` values for the Chat tab badge

A new gRPC API was added:

```text
MarkChatRoomRead(room_id)
```

This API uses the authenticated user from metadata and marks that member read through the latest message sequence in the room.

The older `MarkAsRead(room_id, user_id, last_read_sequence_no)` remains available for compatibility, but the client-facing production path should prefer `MarkChatRoomRead`.

## 14. Adding Chat Push Notification Support

Push support is intentionally scoped to chat messages only.

It does not cover:

- Board notifications
- notice notifications
- the top bell notification surface

The backend now stores FCM device tokens in `chat_device_tokens` with:

- `user_id`
- `device_id`
- `token`
- `platform`
- `created_at`
- `updated_at`
- `last_seen_at`

Clients register and unregister tokens through:

```text
RegisterDeviceToken(device_id, token, platform)
UnregisterDeviceToken(device_id)
```

Both RPCs derive `user_id` from auth metadata. The request body does not contain a user ID, so clients cannot register or unregister another user's token.

When `SendMessage` persists a new message, chat-service:

1. keeps unread state source-of-truth in messages plus `last_read_sequence_no`
2. excludes the sender from push recipients
3. loads active FCM tokens for other active room members
4. sends a chat-only push payload with `type=chat_message`, `room_id`, and `message_id`

Push delivery failure does not fail `SendMessage`.
FCM is disabled unless `CHAT_PUSH_FCM_ENABLED=true` and a Firebase project ID is configured.

## 15. Verification Strategy

This session leaned heavily on incremental verification instead of “finish everything, test later.”

Verification included:

- service tests
- gRPC handler tests
- PostgreSQL repository tests
- migration integration tests
- smoke-client checks
- direct runtime validation against the Docker PostgreSQL instance

At the end of the work, the full suite passed with PostgreSQL-backed tests enabled.

## 16. What This Session Improved

From a user perspective, the service became more reliable in a few important ways:

- sending messages is safer under concurrent usage
- room lists return richer summary data
- room list pagination no longer breaks on rooms with no messages
- reconnect/resume streaming is less likely to lose messages
- multi-user local validation is much easier
- Board-to-Chat entry is idempotent and tied to board context
- unread badges can now be based on backend state instead of client guesses
- chat-message push can now notify other room members without changing the top bell notification surface

From an engineering perspective, the repo became easier to trust because the tricky parts are now covered by stronger tests.

## 17. What Still Comes Next

This session improved correctness, but some work still remains before the service feels fully mature.

Likely next steps:

- verify Flutter reconnect behavior against the new stream semantics
- wire Flutter Board detail to `GetOrCreateBoardChatRoom`
- wire Flutter chat badges to `unread_count` and `MarkChatRoomRead`
- register FCM tokens from the Flutter client after login and route chat push taps by `room_id`
- validate `board_owner_user_id` through Board-service before using it for stronger policy
- consider extending signed read delivery to any future attachment-rich summary/read surfaces beyond direct message responses

## Closing

This was a useful example of what real backend progress often looks like.

Not every valuable change is a new endpoint or a big feature. A lot of the important work is:

- finding race conditions before users do
- aligning implementation with the proto contract
- fixing pagination and null-handling edge cases
- making reconnect behavior predictable
- creating better ways to test the system locally

That is exactly the kind of work that helps a chat backend survive the transition from “works on my machine” to “works when multiple users actually use it.”
