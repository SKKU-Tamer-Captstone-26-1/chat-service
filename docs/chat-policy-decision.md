
# Chat Service Policy Decisions

This document defines the initial policy decisions for the `chat-service`.

The goal of this document is to make product and architecture decisions explicit before implementing database migrations, gRPC contracts, and service logic.

## 1. Service Scope

The chat service supports group chat only.

Out of scope for the initial version:
- direct one-to-one messaging
- typing indicators
- online/offline presence
- message editing
- invitation or approval workflows
- blocking and reporting
- in-service notification delivery

## 2. Room Types

The service supports two room types:

- `GENERAL_GROUP`
- `BOARD_LINKED_GROUP`

A general group room is supported by the service contract.
Current client product flow should not expose arbitrary Chat List room creation unless product policy changes.

A board-linked group room is created from a board post.

Each board post may have at most one linked chat room.

Current product flow for board-linked rooms:

- nearby Board preview belongs to Board/client side
- chat-service does not load nearby Board posts
- Board -> Chat entry calls a board-context chat API
- Chat List must not create arbitrary random chat rooms as a workaround

## 3. Room Creation

Any authenticated user may create a general group chat room.

Any authenticated user may create a board-linked group chat room from a board post.

The creator of the room becomes the room owner.

For Board -> Chat entry, the production API is `GetOrCreateBoardChatRoom`.

It uses authenticated user metadata, receives `board_id`, and returns the existing active linked room or creates one if none exists.

If `board_owner_user_id` is supplied by the client, chat-service may add that user as a member, but this value must be validated through Board-service before it is used for ownership or stronger authorization decisions.

## 4. Room Entry

Room entry is open.

There is no invitation workflow.
There is no approval workflow.

A user may join a room freely unless the user was previously removed from that room.

## 5. Removed Member Re-entry

A member removed by the room owner cannot rejoin the same room.

This policy is represented by the member status:

```text
status = REMOVED
```

The removed member remains in the database for audit and policy enforcement.

## 6. Room Owner Permissions

The room owner may:

remove members
soft delete messages
deactivate the room

These actions must be recorded in a way that supports audit and operational debugging.

## 7. Message Types

The initial service supports the following message types:

TEXT
SYSTEM
IMAGE
FILE

Text messages must be stored as message content.

Image messages may use a primary image URL and additional metadata.

File messages use attachment metadata such as file name, content type, and internal object reference.

System messages are used for service-generated chat events such as room creation, member join, member removal, or room deactivation.

## 8. Message Deletion

Message deletion must use soft deletion.

The message row must remain in the database.

A deleted message should be marked using fields such as:

is_deleted
deleted_at
deleted_by_user_id

The UI may render deleted messages as a placeholder such as:

This message was deleted.

## 9. Room Deletion

Room deletion must use soft deletion or inactive handling.

The room row must remain in the database.

A deactivated room should not accept new messages or new members.

## 10. Read State

The service supports read/unread tracking.

The initial read state model uses room-member level read progress.

The preferred read cursor is:

last_read_sequence_no

Unread messages can be calculated as messages with:

message.sequence_no > member.last_read_sequence_no
AND message.sender_user_id != member.user_id

Detailed per-message read receipts are not required in the initial version.

The client-facing read API is `MarkChatRoomRead(room_id)`.

The server derives the user from auth metadata and advances that member's read cursor to the latest message sequence in the room.

The top bell notification is separate from chat unread state.

## 11. Message Ordering

Messages must have a stable order within each room.

The service should assign a monotonically increasing sequence_no per room.

The combination of room_id and sequence_no must be unique.

## 12. Notification Boundary

Board, notice, and top-bell notification delivery remain out of scope for the chat service.

The chat service now supports chat-message push notifications only.
This is limited to notifying active room members when a new chat message is sent.

Chat push must remain separate from:

- Board notifications
- notice notifications
- top-bell notification state

The chat service may still create events that can later be consumed by a separate notification service.
Any broader notification service must remain separate from chat-service.

Chat device-token RPCs must derive `user_id` from auth metadata:

- `RegisterDeviceToken(device_id, token, platform)`
- `UnregisterDeviceToken(device_id)`

`SendMessage` may dispatch an FCM push to active room members except the sender.
Push delivery failure must not fail message persistence.

## 13. Redis Policy

Redis is not part of the initial implementation.

The first version should use PostgreSQL only.

Redis may be introduced later after performance testing.

Possible future Redis responsibilities:

pub/sub
fan-out support
recent message cache
unread cache

No Redis-specific assumption should be required for the initial domain model.

## 14. Database Policy

PostgreSQL is the initial database.

The service database owns chat-specific entities only:

rooms
room members
messages
chat room events
chat device tokens

The service must not use cross-service database foreign keys.

External entities such as users and board posts are referenced by logical IDs only.

## 15. User Identity Policy

The chat service does not own authentication.

The service uses internal user IDs supplied by the authentication system.

During early development, dev/test user IDs may be used.

Production identity is provided through the authentication layer in `CHAT_AUTH_MODE=validate_token`.

Current production mode supports auth metadata through auth-service token validation.

When auth metadata is present:

- request-body user IDs may be omitted
- if provided, request-body user IDs must match the authenticated principal
- new production-oriented RPCs should avoid request-body `user_id`

## 16. Board Relationship Policy

A board-linked room stores a logical board reference.

The board service remains the source of truth for board post data.

The chat service does not own board content.

The database should enforce that each board post has at most one active linked chat room.

Chat-service stores only a logical board reference, currently `linked_board_id`, and an optional lightweight room title snapshot.

## 17. Development Policy

Initial development should prioritize correctness over scale.

Implementation order should be:

database schema
proto contract
Go service skeleton
room creation
room join
message send
message history
read state
owner moderation
streaming delivery

Performance optimization should come after the baseline service is correct.
