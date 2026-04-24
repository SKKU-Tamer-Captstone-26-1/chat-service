1. For v1, keep `user_id` fields in request bodies.
Auth/JWT metadata integration will come later.
Do not remove body user_id fields yet.
Document that request-body user_id is temporary and must not be treated as production-authoritative.

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
- LEFT users can rejoin
- REMOVED users cannot rejoin
- owner transfer on LeaveRoom
- room deactivation when the last active member leaves
- inactive rooms block JoinRoom, SendMessage, and GetMessages
- deleted messages are returned as placeholders from normal GetMessages without exposing original deleted content
- per-room sequence_no ordering

Additional v1 moderation constraint:
- RemoveMember must reject attempts to remove the current room owner (owner change is handled by LeaveRoom transfer rules)
