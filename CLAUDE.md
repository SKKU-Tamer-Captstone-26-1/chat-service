# CLAUDE.md

This file provides repository-level guidance for Claude when working in the `chat-service` repository.

## Repository Purpose

This repository contains the chat service for On the Block.

The chat service is a standalone backend service in a multi-repository MSA environment.
It is responsible for group chat only.

This service is implemented in Go and uses gRPC for service contracts.
The database is PostgreSQL.
Redis is not part of the initial implementation and may be introduced later based on performance testing.

## Product Scope

This service supports only group chat.

There are two room categories:
- general group chat
- board-linked group chat

A board-linked chat room is associated with a board post.
Each board post may have at most one linked chat room.

Users may:
- create a general group chat room
- create a board-linked group chat room from a board post
- join rooms freely without invitation or approval

## Core Functional Rules

### Room Creation
- Any logged-in user may create a general group chat room.
- Any logged-in user may create a board-linked group chat room from a board post.
- A board post can have at most one linked chat room.

### Room Access
- Room entry is open.
- Invitation and approval workflows are not part of this service.
- Users removed from a room cannot rejoin.

### Room Ownership
The room owner may:
- soft delete messages
- remove members
- soft delete or deactivate the room

### Message Behavior
Supported message types for the initial version:
- TEXT
- SYSTEM
- IMAGE
- FILE

Message edit is not required for the initial version.

Message deletion must be soft deletion.
Deleted records must remain in the database.

### Attachment Storage
- Chat media buckets must remain private.
- Use signed upload URLs for client-to-storage writes.
- Use signed read URLs for attachment delivery to clients.
- Do not log full signed URLs.
- Do not store image or file binaries in PostgreSQL.
- Store only message metadata and internal object references in the database.

### Room Deletion
Room deletion must be soft deletion or inactive handling.
Room records must remain in the database.

### Read State
Read status is required only at the level of read/unread progress.
Precise message-by-message read receipts are not required in the initial version.

### Presence and Typing
Typing indicators, online/offline state, and last seen are out of scope for the initial version.

## Service Boundaries

This repository owns:
- chat rooms
- room membership
- messages
- room-level read state
- chat-domain system events

This repository does not own:
- authentication source of truth
- board source of truth
- notification delivery
- recommendation logic
- user identity provider logic

Authentication and user identity are external concerns.
This service uses externally provided internal user IDs.

The notification system is separate and must not be merged into this service.

## Data Ownership Rules

Use internal user IDs as logical references.
Do not design this service around direct dependency on external provider IDs such as Google OAuth IDs.

Do not assume cross-service foreign keys.
Cross-service relationships must be handled through service contracts and logical identifiers, not database-level FK constraints.

## Contract Rules

Chat proto definitions are initially developed inside this repository.
They may later be promoted into the shared infra/proto repository.

Do not assume proto contracts already exist unless they are present in this repository.
Do not invent hidden cross-service contracts.

Proto definitions should reflect:
- group chat only
- general group and board-linked group room types
- open room join behavior
- soft deletion semantics
- read/unread support
- image and file support in addition to text and system messages

## Persistence Rules

PostgreSQL is the initial database.

The initial data model should prioritize:
- rooms
- room members
- messages
- room status
- read state

Redis is not part of the initial implementation.
Do not introduce Redis-based assumptions into the core design unless explicitly requested.

## Development Philosophy

Prefer:
- clear service boundaries
- minimal but extensible schema design
- soft deletion instead of destructive deletion
- explicit room/member/message modeling
- repository-local proto-first thinking

Avoid:
- premature distributed complexity
- speculative notification implementation inside chat-service
- speculative Redis architecture before performance evidence
- coupling chat logic to board or auth internals

## Communication Rules

Be explicit about assumptions.
Do not present speculative architecture as final fact.
When something is not defined yet, preserve replaceability and document the assumption clearly.

For large design reasoning, prefer markdown documentation over oversized terminal output.
