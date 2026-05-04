CREATE TYPE room_type AS ENUM (
  'GENERAL_GROUP',
  'BOARD_LINKED_GROUP'
);

CREATE TYPE member_role AS ENUM (
  'OWNER',
  'MEMBER'
);

CREATE TYPE member_status AS ENUM (
  'ACTIVE',
  'REMOVED',
  'LEFT'
);

CREATE TYPE message_type AS ENUM (
  'TEXT',
  'SYSTEM',
  'IMAGE',
  'FILE'
);

CREATE TABLE chat_rooms (
  id uuid PRIMARY KEY,
  room_type room_type NOT NULL,
  title varchar(120),
  linked_board_id uuid,
  owner_user_id uuid NOT NULL,
  is_active boolean NOT NULL DEFAULT true,
  deleted_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chat_room_members (
  id uuid PRIMARY KEY,
  room_id uuid NOT NULL,
  user_id uuid NOT NULL,
  role member_role NOT NULL DEFAULT 'MEMBER',
  status member_status NOT NULL DEFAULT 'ACTIVE',
  joined_at timestamptz NOT NULL DEFAULT now(),
  left_at timestamptz,
  removed_at timestamptz,
  removed_by_user_id uuid,
  last_read_sequence_no bigint,
  last_read_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chat_messages (
  id uuid PRIMARY KEY,
  room_id uuid NOT NULL,
  sender_user_id uuid NOT NULL,
  message_type message_type NOT NULL,
  sequence_no bigint NOT NULL,
  content text,
  image_url text,
  metadata_json jsonb,
  is_deleted boolean NOT NULL DEFAULT false,
  deleted_at timestamptz,
  deleted_by_user_id uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chat_room_events (
  id uuid PRIMARY KEY,
  room_id uuid NOT NULL,
  actor_user_id uuid,
  event_type varchar(50) NOT NULL,
  payload_json jsonb,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX chat_rooms_room_type_linked_board_id_idx
  ON chat_rooms (room_type, linked_board_id);

CREATE UNIQUE INDEX chat_rooms_board_active_unique_idx
  ON chat_rooms (linked_board_id)
  WHERE room_type = 'BOARD_LINKED_GROUP'
    AND is_active = true
    AND deleted_at IS NULL;

CREATE INDEX chat_rooms_owner_user_id_is_active_idx
  ON chat_rooms (owner_user_id, is_active);

CREATE INDEX chat_rooms_created_at_idx
  ON chat_rooms (created_at);

CREATE UNIQUE INDEX chat_room_members_room_user_unique_idx
  ON chat_room_members (room_id, user_id);

CREATE INDEX chat_room_members_user_status_idx
  ON chat_room_members (user_id, status);

CREATE INDEX chat_room_members_room_status_idx
  ON chat_room_members (room_id, status);

CREATE INDEX chat_room_members_room_role_idx
  ON chat_room_members (room_id, role);

CREATE UNIQUE INDEX chat_messages_room_sequence_unique_idx
  ON chat_messages (room_id, sequence_no);

CREATE INDEX chat_messages_room_created_at_idx
  ON chat_messages (room_id, created_at);

CREATE INDEX chat_messages_sender_created_at_idx
  ON chat_messages (sender_user_id, created_at);

CREATE INDEX chat_messages_room_deleted_created_at_idx
  ON chat_messages (room_id, is_deleted, created_at);

CREATE INDEX chat_room_events_room_created_at_idx
  ON chat_room_events (room_id, created_at);

CREATE INDEX chat_room_events_event_type_created_at_idx
  ON chat_room_events (event_type, created_at);

ALTER TABLE chat_room_members
  ADD CONSTRAINT chat_room_members_room_fk
  FOREIGN KEY (room_id) REFERENCES chat_rooms (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE chat_messages
  ADD CONSTRAINT chat_messages_room_fk
  FOREIGN KEY (room_id) REFERENCES chat_rooms (id) DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE chat_room_events
  ADD CONSTRAINT chat_room_events_room_fk
  FOREIGN KEY (room_id) REFERENCES chat_rooms (id) DEFERRABLE INITIALLY IMMEDIATE;
