import type { BackupRepository, RepositoryHealth } from '../../api'
import { timeAgo } from '../../api'
import { HealthBar } from '../common/HealthBar'

interface Props {
  repos:  BackupRepository[]
  health: RepositoryHealth[]
  onNew:  () => void
}

// ── Helpers ───────────────────────────────────────────────────────────────────

function typeLabel(t: string): { label: string; icon: string } {
  const MAP: Record<string, { label: string; icon: string }> = {
    'local':       { label: 'Local Path',  icon: '💾' },
    'proxmox':     { label: 'Proxmox',     icon: '🖥' },
    'nas-nfs':     { label: 'NFS / NAS',   icon: '🗄' },
    'nas-smb':     { label: 'NAS / SMB',   icon: '🗄' },
    'minio-s3':    { label: 'MinIO / S3',  icon: '☁' },
    'restic':      { label: 'Restic REST', icon: '⚙' },
    'borg':        { label: 'Borg (SSH)',  icon: '🔒' },
    'pgbackrest':  { label: 'pgBackRest',  icon: '🐘' },
    'velero':      { label: 'Velero',      icon: '☸' },
  }
  return MAP[t] ?? { label: t, icon: '📦' }
}

function immLabel(mode: string, verified: boolean): { text: string; color: string } {
  switch (mode) {
    case 'object_lock': return { text: `🔒 Object Lock (${verified ? 'verified' : 'declared'})`, color: verified ? 'var(--success)' : 'var(--warning)' }
    case 'worm':        return { text: `🔒 WORM (${verified ? 'verified' : 'declared'})`, color: verified ? 'var(--success)' : 'var(--warning)' }
    case 'append_only': return { text: `📎 Append-Only (${verified ? 'verified' : 'declared'})`, color: verified ? 'var(--accent-teal)' : 'var(--warning)' }
    case 'unknown':     return { text: '? Unknown',      color: 'var(--warning)' }
    default:            return { text: '— Disabled',     color: 'var(--text-dim)' }
  }
}

function repoDisplayName(repo: BackupRepository): { name: string; sub: string } {
  const loc = repo.Location
  // Last segment after / or \ as name
  const parts = loc.replace(/\\/g, '/').split('/')
  const last = parts[parts.length - 1] || loc
  return {
    name: last.length > 30 ? '…' + last.slice(-28) : last,
    sub:  typeLabel(repo.Type).label,
  }
}

function repoRegion(repo: BackupRepository): { region: string; sub: string } {
  const loc = repo.Location.toLowerCase()
  if (loc.includes('s3.amazonaws.com') || loc.includes('aws')) return { region: 'AWS S3', sub: 'Cloud' }
  if (loc.includes('blob.core.windows')) return { region: 'Azure Blob', sub: 'Cloud' }
  if (loc.includes('storage.googleapis')) return { region: 'GCS', sub: 'Cloud' }
  if (repo.Type === 'minio-s3') return { region: 'MinIO', sub: 'Self-hosted' }
  if (repo.Type === 'nas-nfs' || repo.Type === 'nas-smb') return { region: 'NAS', sub: 'On-Prem' }
  return { region: 'Local / On-Prem', sub: '' }
}

// ── Component ─────────────────────────────────────────────────────────────────

export function RepositoriesTable({ repos, health, onNew }: Props) {
  return (
    <div style={s.wrap}>
      <div style={s.header}>
        <div style={s.headerLeft}>
          <span style={s.title}>Repositories</span>
          <span style={s.countBadge}>{repos.length}</span>
        </div>
        <button style={s.newBtn} onClick={onNew}>+ New Repository</button>
      </div>

      {repos.length === 0 ? (
        <div style={s.empty}>
          <div style={s.emptyIcon}>📦</div>
          <div style={s.emptyTitle}>No repositories configured.</div>
          <div style={s.emptySub}>Add a repository before creating backup policies.</div>
          <button style={s.emptyBtn} onClick={onNew}>+ New Repository</button>
        </div>
      ) : (
        <div style={s.tableWrap}>
          <table style={s.table}>
            <thead>
              <tr>
                {['Repository', 'Type', 'Region / Location', 'Encryption', 'Immutability / WORM', 'Snapshots', 'Last Verification', 'Health', ''].map(h =>
                  <th key={h} style={s.th}>{h}</th>
                )}
              </tr>
            </thead>
            <tbody>
              {repos.map(repo => {
                const h       = health.find(x => x.RepositoryID === repo.ID)
                const { name, sub } = repoDisplayName(repo)
                const { icon } = typeLabel(repo.Type)
                const { region, sub: rsub } = repoRegion(repo)
                const imm     = immLabel(repo.ImmutableMode, repo.SecurityPosture?.Immutability === 'VERIFIED')
                const vPct    = h && h.SnapshotCount > 0
                  ? Math.round(h.VerifiedCount / h.SnapshotCount * 100)
                  : null

                return (
                  <tr key={repo.ID} style={s.tr}>
                    {/* Repository name */}
                    <td style={s.td}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
                        <span style={s.repoIcon}>{icon}</span>
                        <div>
                          <div style={s.repoName}>{name}</div>
                          <div style={s.repoSub}>{sub}</div>
                        </div>
                      </div>
                    </td>

                    {/* Type */}
                    <td style={s.td}>
                      <span style={s.typeBadge}>{typeLabel(repo.Type).label}</span>
                    </td>

                    {/* Region */}
                    <td style={s.td}>
                      <div style={{ fontSize: 12, color: 'var(--text)' }}>{region}</div>
                      {rsub && <div style={{ fontSize: 10, color: 'var(--text-dim)' }}>{rsub}</div>}
                    </td>

                    {/* Encryption */}
                    <td style={s.td}>
                      {repo.EncryptionMode && repo.SecurityPosture?.Encryption === 'VERIFIED'
                        ? <span style={{ color: 'var(--success)', fontSize: 11, fontWeight: 600 }}>✓ {repo.EncryptionMode}</span>
                        : <span style={{ color: 'var(--warning)', fontSize: 11 }}>⚠ Off</span>}
                    </td>

                    {/* Immutability */}
                    <td style={s.td}>
                      <span style={{ fontSize: 11, color: imm.color, fontWeight: 500 }}>{imm.text}</span>
                    </td>

                    {/* Snapshots */}
                    <td style={s.td}>
                      <span style={{ fontWeight: 600, color: 'var(--text)', fontSize: 12 }}>
                        {h?.SnapshotCount ?? '—'}
                      </span>
                    </td>

                    {/* Last Verification */}
                    <td style={s.td}>
                      {h?.LastRestoreTestAt ? (
                        <div>
                          <div style={{ fontSize: 12, color: 'var(--text)' }}>{timeAgo(h.LastRestoreTestAt)}</div>
                          <div style={{ fontSize: 10, color: 'var(--success)' }}>Successful</div>
                        </div>
                      ) : (
                        <span style={{ fontSize: 11, color: 'var(--text-dim)' }}>never</span>
                      )}
                    </td>

                    {/* Health */}
                    <td style={s.td}><HealthBar pct={vPct} /></td>

                    {/* Actions */}
                    <td style={s.td}>
                      <button style={s.actBtn} title="More actions">•••</button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

const s: Record<string, React.CSSProperties> = {
  wrap:        { background: 'linear-gradient(180deg, rgba(21,28,46,0.95), rgba(10,15,27,0.95))', border: '1px solid rgba(148,163,184,0.12)', borderRadius: 16, overflow: 'hidden' },
  header:      { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '14px 18px', borderBottom: '1px solid var(--border)' },
  headerLeft:  { display: 'flex', alignItems: 'center', gap: 8 },
  title:       { fontSize: 14, fontWeight: 700, color: 'var(--text)' },
  countBadge:  { fontSize: 11, fontWeight: 700, padding: '2px 8px', borderRadius: 10, background: 'rgba(137,189,40,0.1)', color: 'var(--accent)', border: '1px solid rgba(137,189,40,0.2)' },
  newBtn:      { padding: '7px 14px', borderRadius: 8, background: 'rgba(137,189,40,0.1)', border: '1px solid rgba(137,189,40,0.3)', color: 'var(--accent)', fontSize: 12, fontWeight: 600, cursor: 'pointer' },
  tableWrap:   { overflowX: 'auto' },
  table:       { width: '100%', borderCollapse: 'collapse' },
  th:          { padding: '9px 14px', fontSize: 9, fontWeight: 700, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.1em', textAlign: 'left', borderBottom: '1px solid var(--border)', background: 'rgba(255,255,255,0.015)', whiteSpace: 'nowrap' },
  tr:          { borderBottom: '1px solid rgba(255,255,255,0.04)' },
  td:          { padding: '11px 14px', fontSize: 12, color: 'var(--text-muted)', verticalAlign: 'middle' },
  repoIcon:    { fontSize: 18, flexShrink: 0 },
  repoName:    { fontFamily: 'var(--font-mono)', fontSize: 11, fontWeight: 600, color: 'var(--text)' },
  repoSub:     { fontSize: 10, color: 'var(--text-dim)', marginTop: 1 },
  typeBadge:   { fontSize: 10, padding: '2px 7px', borderRadius: 4, background: 'rgba(56,189,248,0.1)', color: 'var(--accent-blue)', fontWeight: 600 },
  actBtn:      { background: 'none', border: 'none', color: 'var(--text-dim)', fontSize: 14, cursor: 'pointer', padding: '2px 6px', letterSpacing: 2 },
  empty:       { padding: '52px 24px', textAlign: 'center' },
  emptyIcon:   { fontSize: 40, opacity: 0.3, marginBottom: 12 },
  emptyTitle:  { fontSize: 15, fontWeight: 600, color: 'var(--text-muted)' },
  emptySub:    { fontSize: 12, color: 'var(--text-dim)', marginTop: 4, marginBottom: 16 },
  emptyBtn:    { padding: '8px 18px', borderRadius: 8, background: 'rgba(137,189,40,0.1)', border: '1px solid rgba(137,189,40,0.3)', color: 'var(--accent)', fontSize: 12, fontWeight: 600, cursor: 'pointer' },
}
