import { useNavigate } from 'react-router-dom'
import type { BackupRepository, RepositoryHealth } from '../../api'
import { timeAgo } from '../../api'

interface Props { repos: BackupRepository[]; health: RepositoryHealth[] }

export function RepositoryHealthTable({ repos, health }: Props) {
  const nav = useNavigate()
  if (repos.length === 0) return null

  return (
    <div className="dash-card" style={s.card}>
      <div style={s.header}>
        <span style={s.title}>Repository Health</span>
        <button style={s.link} onClick={() => nav('/repositories')}>Manage →</button>
      </div>
      <table style={s.table}>
        <thead>
          <tr>
            {['Repository','Type','Protection','Encryption','Snapshots','Verified','Last Backup','Last Restore'].map(h =>
              <th key={h} style={s.th}>{h}</th>)}
          </tr>
        </thead>
        <tbody>
          {repos.map(repo => {
            const h = health.find(x => x.RepositoryID === repo.ID)
            const imm = repo.ImmutableMode ?? 'none'
            const verifiedPct = h && h.SnapshotCount > 0 ? Math.round(h.VerifiedCount / h.SnapshotCount * 100) : 0

            return (
              <tr key={repo.ID} style={s.tr}>
                <td style={s.td}>
                  <div style={s.repoName}>{repo.Location.length > 28 ? '…' + repo.Location.slice(-26) : repo.Location}</div>
                </td>
                <td style={s.td}><TypeBadge type={repo.Type} /></td>
                <td style={s.td}><ImmBadge mode={imm} verified={repo.SecurityPosture?.Immutability === 'VERIFIED'} /></td>
                <td style={s.td}>
                  {repo.EncryptionMode && repo.SecurityPosture?.Encryption === 'VERIFIED'
                    ? <span style={{ color:'var(--success)', fontSize:11 }}>✓ {repo.EncryptionMode}</span>
                    : <span style={{ color:'var(--warning)', fontSize:11 }}>⚠ Off</span>}
                </td>
                <td style={s.td}><span style={s.num}>{h?.SnapshotCount ?? '—'}</span></td>
                <td style={s.td}>
                  {h && h.SnapshotCount > 0 ? (
                    <div style={{ display:'flex', alignItems:'center', gap:6 }}>
                      <div style={{ width:50, height:4, borderRadius:2, background:'var(--border)', overflow:'hidden' }}>
                        <div style={{ width:`${verifiedPct}%`, height:'100%', background: verifiedPct === 100 ? 'var(--success)' : verifiedPct > 50 ? 'var(--warning)' : 'var(--error)', transition:'width 0.3s' }} />
                      </div>
                      <span style={{ fontSize:10, color: verifiedPct === 100 ? 'var(--success)' : 'var(--text-dim)' }}>{verifiedPct}%</span>
                    </div>
                  ) : <span style={{ color:'var(--text-dim)', fontSize:11 }}>—</span>}
                </td>
                <td style={s.td}><span style={s.age}>{timeAgo(h?.LastBackupAt)}</span></td>
                <td style={s.td}><span style={s.age}>{timeAgo(h?.LastRestoreTestAt)}</span></td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function TypeBadge({ type }: { type: string }) {
  return <span style={{ fontSize:10, padding:'2px 6px', borderRadius:4, background:'rgba(56,189,248,0.1)', color:'var(--accent-blue)', fontWeight:600 }}>{type}</span>
}

function ImmBadge({ mode, verified }: { mode: string; verified: boolean }) {
  const cfg: Record<string,{c:string,l:string}> = {
    object_lock: {c:'var(--success)',l:'🔒 Object Lock'},
    worm:        {c:'var(--success)',l:'🔒 WORM'},
    append_only: {c:'var(--accent-teal)',l:'📎 Append-Only'},
    unknown:     {c:'var(--warning)',l:'? Unknown'},
    none:        {c:'var(--text-dim)',l:'— None'},
  }
  const {c,l} = cfg[mode] ?? cfg.none
  return <span style={{ fontSize:10, color: verified ? c : 'var(--warning)', fontWeight:500 }}>{l}{mode !== 'none' && ` (${verified ? 'verified' : 'declared'})`}</span>
}

const s: Record<string, React.CSSProperties> = {
  card:     { overflow:'hidden' },
  header:   { display:'flex', justifyContent:'space-between', alignItems:'center', padding:'14px 18px 10px', borderBottom:'1px solid var(--border)' },
  title:    { fontSize:13, fontWeight:700, color:'var(--text)' },
  link:     { background:'none', border:'none', color:'var(--accent)', fontSize:11, cursor:'pointer', padding:0 },
  table:    { width:'100%', borderCollapse:'collapse' as const },
  th:       { padding:'7px 12px', fontSize:9, fontWeight:700, color:'var(--text-dim)', textTransform:'uppercase' as const, letterSpacing:'0.1em', textAlign:'left' as const, borderBottom:'1px solid var(--border)', background:'rgba(255,255,255,0.015)', whiteSpace:'nowrap' as const },
  tr:       { borderBottom:'1px solid rgba(255,255,255,0.04)' },
  td:       { padding:'9px 12px', fontSize:11, color:'var(--text-muted)', verticalAlign:'middle' as const },
  repoName: { fontFamily:'var(--font-mono)', fontSize:10, color:'var(--text)' },
  num:      { fontWeight:600, color:'var(--text)', fontSize:12 },
  age:      { fontSize:10, color:'var(--text-muted)' },
}
