ALTER TABLE systems
    DROP COLUMN IF EXISTS operation_state_changed_at,
    DROP COLUMN IF EXISTS operation_state;
