
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

A general group room is created directly from the chat page.

A board-linked group room is created from a board post.

Each board post may have at most one linked chat room.

## 3. Room Creation

Any authenticated user may create a general group chat room.

Any authenticated user may create a board-linked group chat room from a board post.

The creator of the room becomes the room owner.

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

Text messages must be stored as message content.

Image messages may use a primary image URL and additional metadata.

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

Detailed per-message read receipts are not required in the initial version.

## 11. Message Ordering

Messages must have a stable order within each room.

The service should assign a monotonically increasing sequence_no per room.

The combination of room_id and sequence_no must be unique.

## 12. Notification Boundary

Notification delivery is out of scope for the chat service.

The chat service may create events that can later be consumed by a separate notification service.

The notification service must remain separate from chat-service.

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

The service must not use cross-service database foreign keys.

External entities such as users and board posts are referenced by logical IDs only.

## 15. User Identity Policy

The chat service does not own authentication.

The service uses internal user IDs supplied by the authentication system.

During early development, dev/test user IDs may be used.

Production identity will be provided through the authentication/JWT layer later.

## 16. Board Relationship Policy

A board-linked room stores a logical board reference.

The board service remains the source of truth for board post data.

The chat service does not own board content.

The database should enforce that each board post has at most one active linked chat room.

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