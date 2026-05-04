DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_type t
    JOIN pg_enum e ON e.enumtypid = t.oid
    WHERE t.typname = 'message_type'
      AND e.enumlabel = 'FILE'
  ) THEN
    ALTER TYPE message_type ADD VALUE 'FILE';
  END IF;
END $$;
