# AGENTS.md

This file defines repository-specific working instructions for coding agents operating in the `chat-service` repository.

## Purpose

The goal of this repository is to implement the On the Block chat service in Go with PostgreSQL and gRPC contracts.

This is a group-chat-only service.
It must remain cleanly separated from auth, notification, board ownership, and recommendation concerns.

For higher-level repository philosophy, follow `CLAUDE.md`.

## Current Implementation Constraints

- Use Go for service implementation.
- Use PostgreSQL for persistence.
- Do not introduce Redis in the initial implementation unless explicitly requested.
- Proto contracts are created in this repository first and may be moved to shared infra later.
- This repository uses internal user IDs supplied by external systems.
- Notification delivery is out of scope for this repository.

## Functional Scope

Implement only the following room categories:
- general group chat
- board-linked group chat

Implement only the following message types initially:
- TEXT
- SYSTEM
- IMAGE
- FILE

Do not implement:
- direct messaging
- typing indicators
- online/offline presence
- message edit
- invitation workflow
- approval workflow
- blocking/reporting system
- in-service notification delivery

## Core Domain Rules

### Room rules
- A board post may have at most one linked chat room.
- Any logged-in user may create a general room.
- Any logged-in user may create a board-linked room from a board post.
- Joining is open.
- No invitation or approval flow exists.
- Removed users cannot rejoin.

### Ownership rules
Room owners may:
- remove members
- soft delete messages
- soft delete or deactivate the room

### Deletion rules
Use soft deletion for:
- messages
- rooms

Deleted data must remain persisted.

### Read rules
Support read/unread tracking.
Initial implementation may use room-member read state rather than detailed per-message receipt rows if that keeps the model simpler.

## Recommended Data Model Direction

Start with these core tables:
- chat_rooms
- chat_room_members
- chat_messages

Recommended additional fields or behavior:
- room type
- board linkage
- owner user ID
- soft deletion fields
- last read message ID or equivalent per member
- monotonically increasing sequence per room for stable ordering

Do not create cross-service foreign keys.

## Proto and API Rules

Use gRPC contracts.
Keep proto definitions local to this repository for now.

Proto contracts should model:
- room creation
- room listing
- room join
- room leave if needed
- message send
- message list/history retrieval
- read-state update
- room member removal by owner
- room soft delete/deactivation by owner

Do not design proto around frontend-only convenience shapes if they distort the service domain.

## Implementation Rules

Prefer:
- small, focused packages
- explicit domain naming
- stable IDs
- clear separation between transport, service logic, and persistence
- room/message/member modeling before optimization

Avoid:
- mixing board ownership logic into chat internals
- embedding auth logic in chat-service
- speculative abstractions for future scale before baseline implementation exists
- broad rewrites without need

## Suggested Repository Shape

A reasonable starting direction is:

- `cmd/`
- `internal/domain/`
- `internal/service/`
- `internal/repository/`
- `internal/transport/grpc/`
- `proto/`
- `docs/`

Adjust only when necessary.
Do not over-engineer the initial structure.

## Testing Rules

Initial implementation should support dev-mode testing with test users.
Do not wait for full production auth integration before enabling local chat testing.

Prefer:
- repository-level unit tests
- service-level tests for room/member/message rules
- basic integration tests for persistence and grpc handlers

## Default Behavior When Information Is Missing

When required details are missing:
- do not invent hidden cross-service dependencies
- preserve replaceability
- use the simplest group-chat-compatible structure
- document assumptions clearly

Default priority:
1. preserve service boundaries
2. preserve data correctness
3. preserve future proto compatibility
4. avoid premature complexity
