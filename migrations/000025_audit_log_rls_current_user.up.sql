-- Repair installs that received migration 13 with the historical hard-coded
-- opensourcebackup role. The control plane migrates with its runtime DATABASE_URL,
-- so binding the policies to CURRENT_USER makes fresh and existing deployments
-- use the same explicitly provisioned application identity.
DROP POLICY IF EXISTS audit_log_select ON audit_log;
DROP POLICY IF EXISTS audit_log_insert ON audit_log;

CREATE POLICY audit_log_select
    ON audit_log
    FOR SELECT
    TO CURRENT_USER
    USING (true);

CREATE POLICY audit_log_insert
    ON audit_log
    FOR INSERT
    TO CURRENT_USER
    WITH CHECK (true);

COMMENT ON TABLE audit_log IS
    'Append-only audit trail. RLS enforced: the migration app user may only INSERT and SELECT. '
    'UPDATE/DELETE require a separately provisioned role. See docs/security/audit-log.md.';
