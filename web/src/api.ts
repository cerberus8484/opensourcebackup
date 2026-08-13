// Empty string = relative URLs (same host/port) — works when UI is served by the control plane.
// Set VITE_API_URL to override for dev (e.g. http://localhost:8080 when running vite dev server).
const BASE = import.meta.env.VITE_API_URL || ''

// Read the CSRF token from the osb_csrf cookie (set by the server on every response).
// The cookie is NOT HttpOnly so JavaScript can read it — required for the Double-Submit pattern.
function csrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)osb_csrf=([^;]+)/)
  return match ? decodeURIComponent(match[1]) : ''
}

function mutatingHeaders(): Record<string, string> {
  return {
    'Content-Type': 'application/json',
    'X-CSRF-Token': csrfToken(),
  }
}

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`${BASE}${path}`)
  if (!res.ok) throw new Error(`${res.status}`)
  return res.json()
}

export async function put<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: 'PUT',
    headers: mutatingHeaders(),
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${res.status}`)
  return res.json()
}

export async function post<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    method: 'POST',
    headers: mutatingHeaders(),
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`${res.status}`)
  return res.json()
}

export async function del(path: string): Promise<void> {
  const res = await fetch(`${BASE}${path}`, {
    method: 'DELETE',
    headers: { 'X-CSRF-Token': csrfToken() },
  })
  if (!res.ok && res.status !== 204) throw new Error(`${res.status}`)
}

export interface System {
  ID: string; Hostname: string; OS?: string; AgentVersion?: string
  LastSeen?: string; RiskClass: string; Tags?: Record<string,string>; CreatedAt: string
}
export type ImmutableMode = 'none' | 'object_lock' | 'worm' | 'append_only' | 'unknown'
export type RepositoryBackendType = 'REST_SERVER' | 'S3' | 'MINIO' | 'FILESYSTEM' | 'UNKNOWN'
export type RepositoryManagementMode = 'LEGACY' | 'MANAGED'
export type SecurityProvenance = 'UNKNOWN' | 'DECLARED' | 'DISCOVERED' | 'VERIFIED'
export interface RepositorySecurityPosture {
  Encryption: SecurityProvenance; ObjectLock: SecurityProvenance; Immutability: SecurityProvenance
}
export type CredentialPurpose = 'BACKUP_WRITE' | 'RESTORE_READ' | 'RETENTION' | 'REPOSITORY_ADMIN'
export interface RepositoryCredentialReference {
  ID: string; RepositoryID: string; Purpose: CredentialPurpose; ProviderType: string; Reference: string
  Status: 'ACTIVE' | 'DISABLED' | 'REVOKED'; CreatedAt: string; UpdatedAt: string
}

export interface ActivityBucket { hour: string; backups: number; restore_tests: number; failures: number; bytes_added?: number }
export interface HealthAlert {
  code: string; severity: 'critical' | 'warning' | 'info'; category: string
  title: string; description: string; points: number; action: string
}
export interface HealthAlertSummary { total: number; critical: number; warning: number; info: number }
export interface AuditEntry {
  ID: number; Timestamp: string; Action: string; ResourceType: string; ResourceID: string
  ActorType: string; Actor: string; IP: string; Details: string
  Severity: 'info' | 'warning' | 'critical'; Success: boolean
}
export interface ScoreDeduction { points: number; code: string; reason: string }
export interface HealthScore {
  score:      number
  label:      string
  color:      string
  version:    string
  deductions: ScoreDeduction[]
  factors:    string[]
}

export interface BackupRepository {
  ID: string; Type: string; Location: string
  BackendType: RepositoryBackendType; ManagementMode: RepositoryManagementMode
  EncryptionMode?: string; ObjectLockEnabled: boolean
  ImmutableMode: ImmutableMode; SecurityPosture: RepositorySecurityPosture; CreatedAt: string
}

export interface RepositoryHealth {
  RepositoryID:       string
  EncryptionEnabled:  boolean
  ImmutableMode:      ImmutableMode
  SnapshotCount:      number
  VerifiedCount:      number
  LastBackupAt?:      string
  LastRestoreTestAt?: string
  LastRetentionAt?:   string
}
export interface ScheduleConfig {
  cron: string; timezone: string
  window_start: string; window_end: string
  if_missed: 'run_asap' | 'skip'
  restore_test_cron: string; retention_cron: string
}

export interface RetentionPlan {
  KeepLast: number; KeepDaily: number; KeepWeekly: number
  KeepMonthly: number; KeepYearly: number
}

export interface AdvancedConfig { BandwidthLimitKbps: number }

export interface BackupPolicy {
  ID: string; Name: string; Engine: string; Schedule?: string
  ScheduleConfig?: ScheduleConfig
  RetentionPlan?: RetentionPlan
  Advanced?: AdvancedConfig
  Includes?: string[]; Excludes?: string[]; RepositoryID?: string; CreatedAt: string
}
export interface BackupJob {
  ID: string; SystemID: string; PolicyID: string; Status: string
  Type?: string  // 'backup' | 'retention'
  BytesUploaded?: number; StartedAt?: string; FinishedAt?: string
  ErrorSummary?: string; CreatedAt: string
  // Live progress (B_JOB_PROGRESS) — aggregate counters only, no file paths
  ProgressPhase?:         string
  ProgressPercent?:       number  // 0..100
  ProgressBytesDone?:     number
  ProgressBytesTotal?:    number
  ProgressFilesDone?:     number
  ProgressFilesTotal?:    number
  ProgressThroughputBps?: number
  LastProgressAt?:        string
  // Cooperative cancellation (B_JOB_CANCEL)
  CancelRequestedAt?: string
  CancelReason?:      string
}
export interface Snapshot {
  ID: string; JobID: string; RepositoryID: string; EngineSnapshotID: string
  Hostname?: string; Paths?: string[]; ChecksumStatus: string; CreatedAt: string
}
export interface RestoreTest {
  ID: string; SnapshotID: string; SystemID: string; RepositoryID: string
  Status: string; TargetPath?: string
  StartedAt?: string; FinishedAt?: string
  VerifiedFiles?: number; VerifiedBytes?: number
  ErrorSummary?: string; CreatedAt: string; UpdatedAt: string
}


export const api = {
  health:        () => get<{status:string}>('/health'),
  systems:       () => get<System[]>('/v1/systems'),
  deleteSystem:  (id: string) => del(`/v1/systems/${id}`),
  deleteJob:     (id: string) => del(`/v1/jobs/${id}`),
  repositories:        () => get<BackupRepository[]>('/v1/repositories'),
  repositoryHealth:    () => get<RepositoryHealth[]>('/v1/repositories/health'),
  healthScore:         () => get<HealthScore>('/v1/health/score'),
  healthActivity:      (hours = 24) => get<ActivityBucket[]>(`/v1/health/activity?hours=${hours}`),
  healthActivityDays:  (days: number) => get<ActivityBucket[]>(`/v1/health/activity?days=${days}`),
  healthActivityWeeks: (weeks: number) => get<ActivityBucket[]>(`/v1/health/activity?weeks=${weeks}`),
  healthAlerts:        () => get<{ alerts: HealthAlert[]; summary: HealthAlertSummary }>('/v1/health/alerts'),
  auditLog:            (limit = 5) => get<AuditEntry[]>(`/v1/audit?limit=${limit}`),
  createRepository:    (r: Partial<BackupRepository> & { ImmutableMode?: ImmutableMode }) => post<BackupRepository>('/v1/repositories', r),
  deleteRepository:    (id: string) => del(`/v1/repositories/${id}`),
  repositoryCredentialReferences: (id: string) => get<RepositoryCredentialReference[]>(`/v1/repositories/${id}/credential-references`),
  policies:      () => get<BackupPolicy[]>('/v1/policies'),
  createPolicy:  (p: Partial<BackupPolicy>) => post<BackupPolicy>('/v1/policies', p),
  updatePolicy:  (id: string, p: Partial<BackupPolicy>) => put<BackupPolicy>(`/v1/policies/${id}`, p),
  deletePolicy:  (id: string) => del(`/v1/policies/${id}`),
  updateSystem:      (id: string, s: Partial<System>) => put<System>(`/v1/systems/${id}`, s),
  updateRepository:  (id: string, r: Partial<BackupRepository>) => put<BackupRepository>(`/v1/repositories/${id}`, r),
  jobs:         () => get<BackupJob[]>('/v1/jobs'),
  snapshots:      () => get<Snapshot[]>('/v1/snapshots'),
  restoreTests:   () => get<RestoreTest[]>('/v1/restore-tests'),
  createRestoreTest: (snapshotID: string, targetPath?: string, repositoryID?: string) =>
    post<RestoreTest>('/v1/restore-tests', { snapshot_id: snapshotID, target_path: targetPath, repository_id: repositoryID || undefined }),
  deleteRestoreTest: (id: string) => del(`/v1/restore-tests/${id}`),
  createJob:    (systemID: string, policyID: string) =>
    post<BackupJob>('/v1/jobs', { SystemID: systemID, PolicyID: policyID, Status: 'pending' }),
  createEnrollmentToken: (systemID: string) =>
    post<{token:string; expires_at:string}>(`/v1/systems/${systemID}/enrollment-token`, {}),
  cancelJob: async (id: string, reason: string): Promise<void> => {
    const res = await fetch(`${BASE}/v1/jobs/${id}/cancel`, {
      method: 'POST',
      headers: mutatingHeaders(),
      body: JSON.stringify({ reason }),
    })
    if (!res.ok) throw new Error(`${res.status}`)
  },
}

export function fmt(bytes?: number) {
  if (!bytes) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024**2) return `${(bytes/1024).toFixed(1)} KB`
  if (bytes < 1024**3) return `${(bytes/1024**2).toFixed(1)} MB`
  return `${(bytes/1024**3).toFixed(2)} GB`
}

export function timeAgo(iso?: string) {
  if (!iso) return 'never'
  const diff = Date.now() - new Date(iso).getTime()
  const m = Math.floor(diff/60000)
  if (m < 1) return 'just now'
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m/60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h/24)}d ago`
}

export function duration(start?: string, end?: string) {
  if (!start || !end) return '—'
  const ms = new Date(end).getTime() - new Date(start).getTime()
  if (ms < 1000) return `${ms}ms`
  if (ms < 60000) return `${(ms/1000).toFixed(1)}s`
  const m = Math.floor(ms/60000); const s = Math.floor((ms%60000)/1000)
  return `${m}m ${s}s`
}
