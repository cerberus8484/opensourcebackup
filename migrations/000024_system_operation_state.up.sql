-- E1.2 System Operation State: an admin-set, persistent operation state per system,
-- separate from the observed health state (which is derived from last_seen, not stored).
-- Existing rows default to 'active' — fully backward compatible.
ALTER TABLE systems
    ADD COLUMN operation_state TEXT NOT NULL DEFAULT 'active'
        CHECK (operation_state IN ('active', 'maintenance', 'draining', 'suspended')),
    ADD COLUMN operation_state_changed_at TIMESTAMPTZ;

-- Actor of a change is recorded in the append-only audit_log (action
-- system.operation_state_changed), NOT duplicated on the system row.
