ALTER TABLE backup_jobs DROP CONSTRAINT IF EXISTS backup_jobs_status_check;

ALTER TABLE backup_jobs ALTER COLUMN status SET DEFAULT 'running';
