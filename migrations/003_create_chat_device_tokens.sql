CREATE TABLE IF NOT EXISTS chat_device_tokens (
  user_id uuid NOT NULL,
  device_id text NOT NULL,
  token text NOT NULL,
  platform varchar(20) NOT NULL CHECK (platform IN ('IOS', 'ANDROID')),
  is_active boolean NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  last_seen_at timestamptz NOT NULL DEFAULT now(),
  unregistered_at timestamptz,
  PRIMARY KEY (user_id, device_id)
);

CREATE INDEX IF NOT EXISTS chat_device_tokens_user_active_idx
  ON chat_device_tokens (user_id, is_active);

CREATE INDEX IF NOT EXISTS chat_device_tokens_token_idx
  ON chat_device_tokens (token);
