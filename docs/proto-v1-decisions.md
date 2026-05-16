1. For v1, keep `user_id` fields in request bodies.
Do not remove body user_id fields yet because local/dev clients and older RPCs still use them.
Document that request-body user_id is temporary and must not be treated as production-authoritative.

Production mode now supports auth metadata through `CHAT_AUTH_MODE=validate_token`.
In that mode, handlers derive the authenticated user from `Authorization: Bearer <access_token>`.
If a request still includes a body user ID, it must match the authenticated user.

2. LeaveRoom behavior for v1
- Users can voluntarily leave a room.
- Leaving sets member status to LEFT.
- LEFT members can rejoin later.
- REMOVED members cannot rejoin.
- If the owner leaves, transfer ownership to the earliest joined remaining ACTIVE member.
- If multiple ACTIVE members have the same joined_at, pick the smallest member_id as deterministic tie-breaker.
- If the new owner leaves later, apply the same transfer rule again.
- If no ACTIVE members remain, deactivate the room using soft delete/inactive handling.
- Do not physically delete room or membership rows.
- Board-linked rooms follow the same leave/rejoin policy as general group rooms.
- When a LEFT member rejoins, update joined_at to the rejoin time.
- Rejoining from LEFT should clear left_at and set status back to ACTIVE.


3. Board-linked uniqueness
Yes. Enforce one active board-linked room per board with a PostgreSQL partial unique index in the migration.

Use the active-room condition:
- room_type = BOARD_LINKED_GROUP
- is_active = true
- deleted_at IS NULL
This means a board can have at most one active linked chat room.

If a linked room is later deactivated, it remains in the database, and a new active room may be created for the same board if needed.

3a. Board-context entry API

Use `GetOrCreateBoardChatRoom` for Board -> Chat entry.

This RPC:
- requires `board_id`
- derives the entering user from auth metadata
- returns the existing active board-linked room when one exists
- creates a new active board-linked room only when none exists
- adds the authenticated user as an active member
- optionally adds `board_owner_user_id` as a member when provided

Chat-service does not fetch nearby Board posts and does not store Board content.
`board_owner_user_id` must be validated through Board-service before it is used for ownership or stronger authorization decisions.

4. Deactivated room policy

No. For v1, `GetMessages` should not work for deactivated rooms through normal user-facing APIs.

When a room is deactivated:
- block `JoinRoom`
- block `SendMessage`
- block `GetMessages`
- keep the room and message rows in the database using soft delete/inactive handling
- do not physically delete data

Message history is preserved for audit/debugging purposes only.
If admin or audit access is needed later, it should be implemented as a separate internal/admin API, not through normal `GetMessages`.

5. Deleted message read API

Change the policy to placeholder behavior.

For v1, GetMessages should return deleted message rows as placeholders, similar to KakaoTalk.

Deleted messages remain soft-deleted in PostgreSQL:
- is_deleted = true
- deleted_at set
- deleted_by_user_id set

GetMessages must include deleted messages in sequence order, but must not expose the original deleted content.

For deleted messages, return:
- message_id
- room_id
- sender_user_id
- message_type
- sequence_no
- is_deleted = true
- sent_at
- deleted_at
- deleted_by_user_id

For deleted messages, do not return:
- original content
- image_url
- sensitive metadata

The client should render them as a deleted-message placeholder.

6. Streaming delivery

Use a pub/sub-based streaming design for v1.

Do not implement DB polling as the main streaming model.

However, Redis should not be a mandatory dependency yet.
Create a small pub/sub abstraction and use an in-memory implementation for the first cut.
The implementation should be replaceable with Redis Pub/Sub later.

SendMessage should:
1. validate room/member rules
2. persist the message to PostgreSQL
3. publish the saved message event to the room subscribers

StreamMessages should subscribe to room-level message events and stream them to connected clients.

If the subscriber's membership is no longer ACTIVE, terminate the stream immediately:
- LEFT -> FAILED_PRECONDITION
- REMOVED -> PERMISSION_DENIED

A user who is LEFT may open a new stream after rejoining.

PostgreSQL remains the source of truth.
Pub/sub is only for real-time delivery.

7. Pagination style

Use different pagination strategies for messages and room lists.

For message history, use sequence-based pagination.
Use `before_sequence_no` and `limit`.
Return `next_before_sequence_no` in the response if more messages exist.
This aligns with per-room `sequence_no`, stable ordering, unread calculation, and efficient `(room_id, sequence_no)` indexing.

For room lists, keep common token/cursor pagination.
The implementation may encode `last_message_at` and `room_id` into the page token.
Do not use offset pagination for chat history.

Do not force message history into generic page_token pagination.

7a. Read/unread state

Use room-member read progress with `last_read_sequence_no`.

`ListMyRooms` returns `unread_count` per room.
Unread count must exclude messages sent by the requesting user.

Use `MarkChatRoomRead(room_id)` as the production client path when a user opens a room.
The server derives the user from auth metadata and advances that member's `last_read_sequence_no` to the latest room message sequence.

The older sequence-specific `MarkAsRead` RPC remains available for compatibility, but clients should prefer `MarkChatRoomRead` when they only need to clear a room after opening it.

7b. Chat push notification state

Chat-service supports chat-message push only.
Do not mix this with Board notifications, notice notifications, or the top bell notification surface.

Device-token RPCs:
- `RegisterDeviceToken(device_id, token, platform)`
- `UnregisterDeviceToken(device_id)`

These RPCs derive `user_id` from auth metadata and do not accept request-body user IDs.

`SendMessage` should:
1. persist the message and keep unread state server-derived from persisted messages
2. find active room members except the sender
3. load active FCM tokens for those users
4. send a push payload with `type=chat_message`, `room_id`, and `message_id`

Push delivery failure should not fail `SendMessage`.
FCM dispatch is disabled unless explicitly configured.

8. Implementation priority

Use incremental tests, not tests only at the end.

Preferred order:
1. migration cleanup
2. migration apply/check test
3. repository layer
4. repository tests
5. service rules
6. service rule tests
7. gRPC handlers
8. handler/integration tests
9. final smoke test

Focus tests especially on:
- one active board-linked room per board
- idempotent `GetOrCreateBoardChatRoom`
- LEFT users can rejoin
- REMOVED users cannot rejoin
- owner transfer on LeaveRoom
- room deactivation when the last active member leaves
- inactive rooms block JoinRoom, SendMessage, and GetMessages
- deleted messages are returned as placeholders from normal GetMessages without exposing original deleted content
- per-room sequence_no ordering
- unread count excludes the user's own messages
- `MarkChatRoomRead` clears room unread state for the authenticated user
- device-token registration uses the authenticated user
- chat-message push excludes the sender and includes `room_id` and `message_id`

Additional v1 moderation constraint:
- RemoveMember must reject attempts to remove the current room owner (owner change is handled by LeaveRoom transfer rules)
