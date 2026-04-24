CREATE TYPE "room_type" AS ENUM (
  'GENERAL_GROUP',
  'BOARD_LINKED_GROUP'
);

CREATE TYPE "member_role" AS ENUM (
  'OWNER',
  'MEMBER'
);

CREATE TYPE "member_status" AS ENUM (
  'ACTIVE',
  'REMOVED',
  'LEFT'
);

CREATE TYPE "message_type" AS ENUM (
  'TEXT',
  'SYSTEM',
  'IMAGE'
);

CREATE TABLE "chat_rooms" (
  "id" uuid PRIMARY KEY,
  "room_type" room_type NOT NULL,
  "title" varchar(120),
  "linked_board_id" uuid,
  "owner_user_id" uuid NOT NULL,
  "is_active" boolean NOT NULL DEFAULT true,
  "deleted_at" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "chat_room_members" (
  "id" uuid PRIMARY KEY,
  "room_id" uuid NOT NULL,
  "user_id" uuid NOT NULL,
  "role" member_role NOT NULL DEFAULT 'MEMBER',
  "status" member_status NOT NULL DEFAULT 'ACTIVE',
  "joined_at" timestamptz NOT NULL DEFAULT (now()),
  "left_at" timestamptz,
  "removed_at" timestamptz,
  "removed_by_user_id" uuid,
  "last_read_sequence_no" bigint,
  "last_read_at" timestamptz,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "chat_messages" (
  "id" uuid PRIMARY KEY,
  "room_id" uuid NOT NULL,
  "sender_user_id" uuid NOT NULL,
  "message_type" message_type NOT NULL,
  "sequence_no" bigint NOT NULL,
  "content" text,
  "image_url" text,
  "metadata_json" jsonb,
  "is_deleted" boolean NOT NULL DEFAULT false,
  "deleted_at" timestamptz,
  "deleted_by_user_id" uuid,
  "created_at" timestamptz NOT NULL DEFAULT (now()),
  "updated_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE TABLE "chat_room_events" (
  "id" uuid PRIMARY KEY,
  "room_id" uuid NOT NULL,
  "actor_user_id" uuid,
  "event_type" varchar(50) NOT NULL,
  "payload_json" jsonb,
  "created_at" timestamptz NOT NULL DEFAULT (now())
);

CREATE INDEX ON "chat_rooms" ("room_type", "linked_board_id");

CREATE INDEX ON "chat_rooms" ("owner_user_id", "is_active");

CREATE INDEX ON "chat_rooms" ("created_at");

CREATE UNIQUE INDEX ON "chat_room_members" ("room_id", "user_id");

CREATE INDEX ON "chat_room_members" ("user_id", "status");

CREATE INDEX ON "chat_room_members" ("room_id", "status");

CREATE INDEX ON "chat_room_members" ("room_id", "role");

CREATE UNIQUE INDEX ON "chat_messages" ("room_id", "sequence_no");

CREATE INDEX ON "chat_messages" ("room_id", "created_at");

CREATE INDEX ON "chat_messages" ("sender_user_id", "created_at");

CREATE INDEX ON "chat_messages" ("room_id", "is_deleted", "created_at");

CREATE INDEX ON "chat_room_events" ("room_id", "created_at");

CREATE INDEX ON "chat_room_events" ("event_type", "created_at");

COMMENT ON TABLE "chat_rooms" IS 'linked_board_id must be unique when room_type = BOARD_LINKED_GROUP
and deleted_at is null. This should be enforced with a partial unique index in PostgreSQL.
General group chats may have a custom title.
Board-linked chats may inherit title/display from the board side.
';

COMMENT ON COLUMN "chat_rooms"."linked_board_id" IS 'nullable; set only for board-linked rooms';

COMMENT ON COLUMN "chat_rooms"."owner_user_id" IS 'internal user id from auth service';

COMMENT ON TABLE "chat_room_members" IS 'A removed user cannot rejoin.
ACTIVE = current participant
LEFT = voluntarily left
REMOVED = kicked by owner
last_read_sequence_no is preferred over last_read_message_id for simpler unread count calculation.
';

COMMENT ON COLUMN "chat_room_members"."user_id" IS 'internal user id from auth service';

COMMENT ON COLUMN "chat_room_members"."removed_by_user_id" IS 'nullable; owner who removed this member';

COMMENT ON COLUMN "chat_room_members"."last_read_sequence_no" IS 'nullable; latest read sequence in this room';

COMMENT ON TABLE "chat_messages" IS 'Soft delete only.
Keep rows for audit/history.
sequence_no should be assigned per room.
IMAGE messages may use image_url plus metadata_json for extensibility.
';

COMMENT ON COLUMN "chat_messages"."sender_user_id" IS 'internal user id from auth service';

COMMENT ON COLUMN "chat_messages"."sequence_no" IS 'monotonic per room for stable ordering';

COMMENT ON COLUMN "chat_messages"."content" IS 'nullable for image/system cases if metadata is used';

COMMENT ON COLUMN "chat_messages"."image_url" IS 'nullable; initial image support';

COMMENT ON COLUMN "chat_messages"."metadata_json" IS 'optional extra payload for system/image messages';

COMMENT ON COLUMN "chat_messages"."deleted_by_user_id" IS 'nullable; usually owner who deleted it';

COMMENT ON TABLE "chat_room_events" IS 'Optional but recommended for audit trails, moderation actions, and notification trigger points.
Can coexist with SYSTEM messages.
';

COMMENT ON COLUMN "chat_room_events"."actor_user_id" IS 'nullable for system-generated events';

COMMENT ON COLUMN "chat_room_events"."event_type" IS 'e.g. ROOM_CREATED, MEMBER_JOINED, MEMBER_REMOVED, ROOM_DELETED';

ALTER TABLE "chat_room_members" ADD FOREIGN KEY ("room_id") REFERENCES "chat_rooms" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "chat_messages" ADD FOREIGN KEY ("room_id") REFERENCES "chat_rooms" ("id") DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE "chat_room_events" ADD FOREIGN KEY ("room_id") REFERENCES "chat_rooms" ("id") DEFERRABLE INITIALLY IMMEDIATE;
