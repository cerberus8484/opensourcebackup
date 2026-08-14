DROP TABLE IF EXISTS repository_credential_references;

ALTER TABLE repositories
    DROP CONSTRAINT IF EXISTS repositories_immutability_posture_check,
    DROP CONSTRAINT IF EXISTS repositories_object_lock_posture_check,
    DROP CONSTRAINT IF EXISTS repositories_encryption_posture_check,
    DROP CONSTRAINT IF EXISTS repositories_management_mode_check,
    DROP CONSTRAINT IF EXISTS repositories_backend_type_check,
    DROP COLUMN IF EXISTS immutability_posture,
    DROP COLUMN IF EXISTS object_lock_posture,
    DROP COLUMN IF EXISTS encryption_posture,
    DROP COLUMN IF EXISTS management_mode,
    DROP COLUMN IF EXISTS backend_type;
