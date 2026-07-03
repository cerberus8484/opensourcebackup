-- Pin the job status vocabulary and correct the default.
-- Part of B_STABILIZATION_CORE slice 1: the guarded job lifecycle. The state
-- machine is enforced in the store (catalog.pgJobStore transition methods); this
-- constraint is the last line of defence against a rogue/typo'd status ever
-- reaching the table.
--
-- Existing rows only ever use these five values, so the CHECK is safe to add.

ALTER TABLE backup_jobs ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE backup_jobs
    ADD CONSTRAINT backup_jobs_status_check
    CHECK (status IN ('pending', 'running', 'success', 'failed', 'cancelled'));
