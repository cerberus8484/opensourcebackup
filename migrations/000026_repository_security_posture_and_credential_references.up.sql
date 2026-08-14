-- E2.1a: repository identity is classified separately from legacy type labels.
-- Security posture is provenance only: this migration never marks a capability VERIFIED.
ALTER TABLE repositories
    ADD COLUMN IF NOT EXISTS backend_type TEXT NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS management_mode TEXT NOT NULL DEFAULT 'LEGACY',
    ADD COLUMN IF NOT EXISTS encryption_posture TEXT NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS object_lock_posture TEXT NOT NULL DEFAULT 'UNKNOWN',
    ADD COLUMN IF NOT EXISTS immutability_posture TEXT NOT NULL DEFAULT 'UNKNOWN';

UPDATE repositories
SET backend_type = CASE
    WHEN lower(trim(type)) IN ('rest_server', 'rest-server', 'rest server') OR lower(location) LIKE 'rest:%' THEN 'REST_SERVER'
    WHEN lower(trim(type)) IN ('s3', 'aws-s3', 'aws_s3') OR lower(location) LIKE 's3:%' OR lower(location) LIKE 's3://%' THEN 'S3'
    WHEN lower(trim(type)) IN ('minio', 'minio-s3', 'minio_s3') THEN 'MINIO'
    WHEN lower(trim(type)) IN ('local', 'filesystem', 'file_system', 'nas-nfs', 'nas-smb', 'proxmox')
      OR location LIKE '/%' OR location LIKE '\\\\%' THEN 'FILESYSTEM'
    ELSE 'UNKNOWN'
END,
management_mode = 'LEGACY',
encryption_posture = CASE WHEN encryption_mode IS NOT NULL AND btrim(encryption_mode) <> '' THEN 'DECLARED' ELSE 'UNKNOWN' END,
object_lock_posture = CASE WHEN object_lock_enabled THEN 'DECLARED' ELSE 'UNKNOWN' END,
immutability_posture = CASE WHEN immutable_mode IN ('object_lock', 'worm', 'append_only') THEN 'DECLARED' ELSE 'UNKNOWN' END;

ALTER TABLE repositories
    ADD CONSTRAINT repositories_backend_type_check CHECK (backend_type IN ('REST_SERVER', 'S3', 'MINIO', 'FILESYSTEM', 'UNKNOWN')),
    ADD CONSTRAINT repositories_management_mode_check CHECK (management_mode IN ('LEGACY', 'MANAGED')),
    ADD CONSTRAINT repositories_encryption_posture_check CHECK (encryption_posture IN ('UNKNOWN', 'DECLARED', 'DISCOVERED', 'VERIFIED')),
    ADD CONSTRAINT repositories_object_lock_posture_check CHECK (object_lock_posture IN ('UNKNOWN', 'DECLARED', 'DISCOVERED', 'VERIFIED')),
    ADD CONSTRAINT repositories_immutability_posture_check CHECK (immutability_posture IN ('UNKNOWN', 'DECLARED', 'DISCOVERED', 'VERIFIED'));

CREATE TABLE repository_credential_references (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    repository_id UUID NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    purpose TEXT NOT NULL CHECK (purpose IN ('BACKUP_WRITE', 'RESTORE_READ', 'RETENTION', 'REPOSITORY_ADMIN')),
    provider_type TEXT NOT NULL,
    reference TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISABLED', 'REVOKED')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (repository_id, purpose)
);

CREATE INDEX repository_credential_references_repository_id_idx ON repository_credential_references(repository_id);

COMMENT ON TABLE repository_credential_references IS
    'Credential reference metadata only. No secret value, token, password, or private key may be stored here.';
