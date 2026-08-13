// Repository summary card for the cockpit sidebar: name, type/path, capability
// badges, and the last successful backup that targeted this repository.
import { fmt, timeAgo, type BackupRepository } from '../../api'
import { friendlyLoc } from './util'

export interface RepoLastSuccess {
  at:     string
  bytes?: number
}

interface RepositoryCardProps {
  repo:        BackupRepository
  lastSuccess: RepoLastSuccess | null
  onDetails:   () => void
}

export function RepositoryCard({ repo, lastSuccess, onDetails }: RepositoryCardProps) {
  const encrypted = !!repo.EncryptionMode && repo.EncryptionMode !== 'none' && repo.SecurityPosture?.Encryption === 'VERIFIED'
  const immutable = repo.ImmutableMode !== 'none' && repo.ImmutableMode !== 'unknown' && repo.SecurityPosture?.Immutability === 'VERIFIED'

  return (
    <div style={s.card}>
      <div style={s.head}>
        <div style={s.iconCircle}>🗄</div>
        <div style={s.title}>{friendlyLoc(repo.Location)}</div>
        <span style={s.typeChip}>{repo.Type}</span>
      </div>

      <div style={s.path} title={repo.Location}>{repo.Location}</div>

      <div style={s.badges}>
        {encrypted
          ? <span style={{ ...s.badge, ...s.green }}>🔑 encrypted</span>
          : <span style={{ ...s.badge, ...s.orange }}>⚠ unverschlüsselt</span>}
        {immutable
          ? <span style={{ ...s.badge, ...s.purple }}>🔒 immutable</span>
          : <span style={{ ...s.badge, ...s.orange }}>kein Lock</span>}
        {repo.ObjectLockEnabled && <span style={{ ...s.badge, ...s.green }}>Object-Lock</span>}
      </div>

      <div style={s.divider} />

      <div style={s.lastRow}>
        <span style={s.lastLabel}>Letzter erfolgreicher Job</span>
        {lastSuccess ? (
          <span style={s.lastVal}>vor {timeAgo(lastSuccess.at)} · {fmt(lastSuccess.bytes)}</span>
        ) : (
          <span style={s.lastNone}>—</span>
        )}
      </div>

      <button style={s.link} onClick={onDetails}>Details anzeigen →</button>
    </div>
  )
}

const s: Record<string, React.CSSProperties> = {
  card:      { background: 'linear-gradient(180deg, rgba(21,28,46,0.95) 0%, rgba(10,15,27,0.95) 100%)', border: '1px solid var(--border)', borderRadius: 14, padding: 16, display: 'flex', flexDirection: 'column', gap: 10 },
  head:      { display: 'flex', alignItems: 'center', gap: 10 },
  iconCircle:{ width: 34, height: 34, borderRadius: 10, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 16, background: 'rgba(34,197,94,0.1)', color: 'var(--success)', flexShrink: 0 },
  title:     { fontSize: 14, fontWeight: 700, color: 'var(--text)', flex: 1, minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  typeChip:  { fontSize: 10, fontWeight: 700, color: 'var(--text-muted)', textTransform: 'uppercase', letterSpacing: '0.06em', background: 'rgba(255,255,255,0.05)', padding: '3px 8px', borderRadius: 6, flexShrink: 0 },
  path:      { fontSize: 11, color: 'var(--text-dim)', fontFamily: 'var(--font-mono)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' },
  badges:    { display: 'flex', gap: 6, flexWrap: 'wrap' },
  badge:     { padding: '3px 8px', borderRadius: 6, fontSize: 10, fontWeight: 700, letterSpacing: '0.02em' },
  green:     { background: 'rgba(137,189,40,0.1)',  color: 'var(--accent)', border: '1px solid rgba(137,189,40,0.25)' },
  purple:    { background: 'rgba(139,92,246,0.12)', color: '#a78bfa', border: '1px solid rgba(139,92,246,0.22)' },
  orange:    { background: 'rgba(245,158,11,0.1)',  color: 'var(--warning)', border: '1px solid rgba(245,158,11,0.2)' },
  divider:   { height: 1, background: 'rgba(255,255,255,0.05)' },
  lastRow:   { display: 'flex', flexDirection: 'column', gap: 3 },
  lastLabel: { fontSize: 11, color: 'var(--text-dim)' },
  lastVal:   { fontSize: 13, fontWeight: 600, color: 'var(--success)' },
  lastNone:  { fontSize: 13, color: 'var(--text-dim)' },
  link:      { alignSelf: 'flex-start', background: 'none', border: 'none', padding: 0, color: 'var(--running)', fontSize: 12, fontWeight: 600, cursor: 'pointer' },
}
