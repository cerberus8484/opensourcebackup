package catalog

import (
	"time"

	"github.com/google/uuid"
)

type System struct {
	ID           uuid.UUID
	Hostname     string
	OS           *string
	AgentVersion *string
	LastSeen     *time.Time
	Tags         map[string]any
	RiskClass    string
	// OperationState is the admin-set operation state (E1.2): active | maintenance |
	// draining | suspended. Governs whether new jobs may be dispatched. Separate
	// from the observed (derived, non-stored) health state.
	OperationState          string
	OperationStateChangedAt *time.Time
	CreatedAt               time.Time
}

// ImmutableMode documents the write-protection mechanism of a repository.
// This is a declared property — not automatically verified against the storage backend.
type ImmutableMode string

const (
	ImmutableNone       ImmutableMode = "none"        // no write protection
	ImmutableObjectLock ImmutableMode = "object_lock" // S3/MinIO Object Lock
	ImmutableWORM       ImmutableMode = "worm"        // hardware/NAS WORM
	ImmutableAppendOnly ImmutableMode = "append_only" // restic --append-only or similar
	ImmutableUnknown    ImmutableMode = "unknown"     // not verified
)

// IsProtected reports whether this mode provides any form of write protection.
func (m ImmutableMode) IsProtected() bool {
	return m != ImmutableNone && m != ImmutableUnknown && m != ""
}

type BackupRepository struct {
	ID                uuid.UUID
	Type              string
	Location          string
	EncryptionMode    *string
	ObjectLockEnabled bool
	ImmutableMode     ImmutableMode // preferred over ObjectLockEnabled for new code
	RetentionPolicyID *uuid.UUID
	CreatedAt         time.Time
}

// RepositoryHealth holds derived health indicators for a repository.
// Computed on demand — never stored. Honest: no fake "healthy" without evidence.
type RepositoryHealth struct {
	RepositoryID      uuid.UUID
	EncryptionEnabled bool
	ImmutableMode     ImmutableMode
	SnapshotCount     int
	VerifiedCount     int // snapshots with successful restore test
	LastBackupAt      *time.Time
	LastRestoreTestAt *time.Time
	LastRetentionAt   *time.Time
}

// RetentionPlan holds the restic-compatible keep rules for a policy.
// All zero values mean "no limit" for that dimension.
// The safety rule (never delete the last restore-tested snapshot) is enforced
// by the control plane during retention job validation — independent of these values.
type RetentionPlan struct {
	KeepLast    int `json:"keep_last,omitempty"`
	KeepDaily   int `json:"keep_daily,omitempty"`
	KeepWeekly  int `json:"keep_weekly,omitempty"`
	KeepMonthly int `json:"keep_monthly,omitempty"`
	KeepYearly  int `json:"keep_yearly,omitempty"`
}

// HasRules reports whether any keep rule is configured.
func (r RetentionPlan) HasRules() bool {
	return r.KeepLast > 0 || r.KeepDaily > 0 || r.KeepWeekly > 0 ||
		r.KeepMonthly > 0 || r.KeepYearly > 0
}

// ScheduleConfig holds the full scheduling configuration for a policy.
// All fields are optional — empty means "not configured".
type ScheduleConfig struct {
	// Backup schedule (cron expression, e.g. "0 2 * * *")
	Cron     string `json:"cron"`
	Timezone string `json:"timezone"` // IANA, e.g. "Europe/Berlin"

	// Backup window: only run backups between these times (HH:MM)
	WindowStart string `json:"window_start"` // e.g. "22:00"
	WindowEnd   string `json:"window_end"`   // e.g. "06:00"

	// What to do if a scheduled backup is missed
	IfMissed string `json:"if_missed"` // "run_asap" | "skip"

	// Separate schedules for restore tests, retention/prune, and verify
	RestoreTestCron string `json:"restore_test_cron"`
	RetentionCron   string `json:"retention_cron"`
	VerifyCron      string `json:"verify_cron"`
	VerifyFull      bool   `json:"verify_full"` // --read-data
}

// AdvancedConfig holds performance and auto-update settings.
type AdvancedConfig struct {
	BandwidthLimitKbps int `json:"bandwidth_limit_kbps"` // 0 = unlimited
}

type BackupPolicy struct {
	ID             uuid.UUID
	Name           string
	Includes       []string
	Excludes       []string
	Schedule       *string // legacy cron field — use ScheduleConfig.Cron for new code
	ScheduleConfig ScheduleConfig
	Advanced       AdvancedConfig
	Retention      map[string]any
	RetentionPlan  RetentionPlan
	Engine         string
	PreHooks       []string
	PostHooks      []string
	RepositoryID   *uuid.UUID
	CreatedAt      time.Time
}

const (
	JobTypeBackup    = "backup"
	JobTypeRetention = "retention"
)

// RestoreTest records the result of verifying a snapshot can be restored.
type RestoreTest struct {
	ID            uuid.UUID
	SnapshotID    uuid.UUID
	SystemID      uuid.UUID
	RepositoryID  uuid.UUID
	Status        string
	TargetPath    *string
	StartedAt     *time.Time
	FinishedAt    *time.Time
	VerifiedFiles *int
	VerifiedBytes *int64
	ErrorSummary  *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BackupJob and Snapshot are models only — repositories added in B3.

type BackupJob struct {
	ID            uuid.UUID
	SystemID      uuid.UUID
	PolicyID      uuid.UUID
	Type          string // "backup" | "retention" — see JobTypeBackup / JobTypeRetention
	StartedAt     *time.Time
	FinishedAt    *time.Time
	Status        string
	BytesScanned  *int64
	BytesUploaded *int64
	ErrorSummary  *string
	RawOutput     map[string]any
	CreatedAt     time.Time

	// Live progress (B_JOB_PROGRESS) — updated while a backup runs. Aggregate
	// numbers only; no file paths are ever stored (privacy / data minimisation).
	ProgressPhase         string
	ProgressPercent       float64 // 0..100
	ProgressBytesDone     int64
	ProgressBytesTotal    int64
	ProgressFilesDone     int
	ProgressFilesTotal    int
	ProgressThroughputBps int64
	LastProgressAt        *time.Time

	// Cooperative cancellation (B_JOB_CANCEL). CancelRequestedAt is set when an
	// operator requests a stop; the agent observes it and reports status "cancelled".
	CancelRequestedAt *time.Time
	CancelReason      string
}

// JobProgress is a live progress snapshot reported by the agent during a backup.
// It deliberately carries NO file paths/names (restic's current_files) — only
// aggregate counters — to keep reporting privacy-safe (DSGVO data minimisation).
type JobProgress struct {
	Phase         string  `json:"phase"`
	Percent       float64 `json:"percent"` // 0..100
	BytesDone     int64   `json:"bytes_done"`
	BytesTotal    int64   `json:"total_bytes"`
	FilesDone     int     `json:"files_done"`
	FilesTotal    int     `json:"total_files"`
	ThroughputBps int64   `json:"throughput_bps"`
}

type Snapshot struct {
	ID               uuid.UUID
	JobID            uuid.UUID
	EngineSnapshotID string
	RepositoryID     uuid.UUID
	CreatedAt        time.Time
	Hostname         *string
	Paths            []string
	ChecksumStatus   string
}
