import { useEffect, useState } from 'react'
import {
  api, timeAgo, duration,
  type BackupJob, type RestoreTest, type Snapshot, type System,
  type BackupRepository, type RepositoryHealth, type HealthScore, type ActivityBucket, type AuditEntry, type HealthAlert
} from '../api'
import { Topbar }                   from '../components/Topbar'
import { KpiCard }                  from '../components/dashboard/KpiCard'
import { HealthScoreCard }          from '../components/dashboard/HealthScoreCard'
import { RecentJobsTable }          from '../components/dashboard/RecentJobsTable'
import { AgentActivityCard }        from '../components/dashboard/AgentActivityCard'
import { AlertsPreview }            from '../components/dashboard/AlertsPreview'
import { RepositoryHealthTable }    from '../components/dashboard/RepositoryHealthTable'
import { RestoreVerificationDonut } from '../components/dashboard/RestoreVerificationDonut'
import { ActivityChart, ActivityLegend } from '../components/ActivityChart'

// used in sub-components via import from '../../api'
void timeAgo; void duration;

export function Dashboard() {
  const [systems,      setSystems]      = useState<System[]>([])
  const [jobs,         setJobs]         = useState<BackupJob[]>([])
  const [snapshots,    setSnapshots]    = useState<Snapshot[]>([])
  const [restoreTests, setRestoreTests] = useState<RestoreTest[]>([])
  const [repos,        setRepos]        = useState<BackupRepository[]>([])
  const [repoHealth,   setRepoHealth]   = useState<RepositoryHealth[]>([])
  const [healthScore,  setHealthScore]  = useState<HealthScore|null>(null)
  const [activity,     setActivity]     = useState<ActivityBucket[]>([])
  const [actRange,     setActRange]     = useState<'24h'|'7d'|'30d'|'1y'>('24h')
  const [alerts,       setAlerts]       = useState<HealthAlert[]>([])
  const [evidence,     setEvidence]     = useState<AuditEntry[]>([])
  const [loading,      setLoading]      = useState(true)

  const loadActivity = (range: '24h'|'7d'|'30d'|'1y') => {
    const p = range === '24h' ? api.healthActivity(24)
            : range === '7d'  ? api.healthActivityDays(7)
            : range === '30d' ? api.healthActivityDays(30)
            : api.healthActivityWeeks(52)
    p.then(setActivity).catch(() => setActivity([]))
  }

  useEffect(() => {
    Promise.all([
      api.systems(), api.jobs(), api.snapshots(), api.restoreTests(),
      api.repositories(),
      api.repositoryHealth().catch(() => [] as RepositoryHealth[]),
      api.healthScore().catch(() => null),
      api.healthActivity(24).catch(() => []),
      api.healthAlerts().catch(() => ({ alerts: [], summary: {} })),
      api.auditLog(8).catch(() => []),
    ]).then(([sy, j, sn, rt, r, rh, hs, act, al, ev]) => {
      setSystems(sy)
      setJobs(j)
      setSnapshots(sn)
      setRestoreTests(rt)
      setRepos(r)
      setRepoHealth(rh)
      setHealthScore(hs)
      setActivity(act)
      setAlerts(al.alerts)
      setEvidence(ev)
    }).finally(() => setLoading(false))

    // Live-refresh: systems + jobs every 30s so LastSeen / agent status stay current
    const tick = setInterval(() => {
      api.systems().then(setSystems).catch(() => {})
      api.jobs().then(setJobs).catch(() => {})
    }, 30_000)
    return () => clearInterval(tick)
  }, [])

  // ── Derived ───────────────────────────────────────────────────────────────
  const [renderedAt] = useState(Date.now)
  const successJobs   = jobs.filter(j => j.Status === 'success').length
  const failedJobs    = jobs.filter(j => j.Status === 'failed').length
  const failedLast24h = jobs.filter(j =>
    j.Status === 'failed' && (renderedAt - new Date(j.CreatedAt).getTime()) < 86_400_000
  ).length
  const successRate   = jobs.length > 0 ? Math.round(successJobs / jobs.length * 100) : 0
  const verifiedSnaps = snapshots.filter(sn =>
    restoreTests.some(rt => rt.SnapshotID === sn.ID && rt.Status === 'success')
  )
  const restoreVerifiedPct = snapshots.length > 0
    ? Math.round(verifiedSnaps.length / snapshots.length * 100) : 0

  const backupSparkline = activity.map(b => b.backups)
  const failSparkline   = activity.map(b => b.failures)

  // Throughput: bytes uploaded in last 24h and 7d
  const bytes24h = jobs
    .filter(j => j.Status === 'success' && (renderedAt - new Date(j.CreatedAt).getTime()) < 86_400_000)
    .reduce((a, j) => a + (j.BytesUploaded ?? 0), 0)
  const bytes7d = jobs
    .filter(j => j.Status === 'success' && (renderedAt - new Date(j.CreatedAt).getTime()) < 7 * 86_400_000)
    .reduce((a, j) => a + (j.BytesUploaded ?? 0), 0)
  const systemMap       = Object.fromEntries(systems.map(s => [s.ID, s.Hostname]))
  const recentJobs      = [...jobs]
    .sort((a, b) => new Date(b.CreatedAt).getTime() - new Date(a.CreatedAt).getTime())
    .slice(0, 8)
  const notifCount = alerts.filter(alert => alert.severity === 'critical' || alert.severity === 'warning').length

  if (loading) return (
    <div style={{ display:'flex', alignItems:'center', justifyContent:'center', height:'100%', color:'var(--text-muted)', fontSize:13 }}>
      Loading…
    </div>
  )

  return (
    <div style={s.page}>
      <Topbar
        title="Dashboard"
        sub="Real-time overview of your backup posture and restore assurance."
        alertCount={notifCount}
      />

      <div style={s.content}>

        {/* ── KPI Row ──────────────────────────────────────────────────── */}
        <div style={s.kpiRow}>
          <KpiCard icon="🖥" label="Protected Systems"
            value={String(systems.length)}
            sub={`${systems.length} system${systems.length !== 1 ? 's' : ''} enrolled`}
            color="var(--accent-blue)"
          />
          <KpiCard icon="✓" label="Backup Success Rate"
            value={jobs.length > 0 ? `${successRate}%` : '—'}
            sub={jobs.length > 0 ? `${successJobs} of ${jobs.length} jobs` : 'No jobs yet'}
            color={successRate >= 90 ? 'var(--success)' : successRate >= 70 ? 'var(--warning)' : jobs.length > 0 ? 'var(--error)' : 'var(--text-dim)'}
            sparkData={backupSparkline} sparkColor="var(--success)"
            trend={jobs.length > 0 ? `${successJobs} successful` : undefined}
            trendUp={successRate >= 90}
          />
          <KpiCard icon="🔄" label="Restore Verified"
            value={snapshots.length > 0 ? `${restoreVerifiedPct}%` : '—'}
            sub={snapshots.length > 0 ? `${verifiedSnaps.length} of ${snapshots.length} snapshots` : 'No snapshots yet'}
            color={restoreVerifiedPct === 100 ? 'var(--success)' : restoreVerifiedPct > 0 ? 'var(--warning)' : snapshots.length > 0 ? 'var(--error)' : 'var(--text-dim)'}
            warn={snapshots.length > 0 && restoreVerifiedPct === 0}
          />
          <KpiCard icon="⚠" label="Failed Jobs"
            value={String(failedJobs)}
            sub={failedLast24h > 0 ? `${failedLast24h} in last 24h` : 'None in last 24h'}
            color={failedJobs > 0 ? 'var(--error)' : 'var(--text-dim)'}
            sparkData={failSparkline} sparkColor="var(--error)"
            warn={failedJobs > 0}
          />
          <KpiCard icon="📦" label="Data Backed Up (24h)"
            value={fmtBytes(bytes24h)}
            sub={bytes7d > 0 ? `${fmtBytes(bytes7d)} in last 7 days` : 'No backups yet'}
            color="var(--accent-teal)"
            sparkData={backupSparkline} sparkColor="var(--accent-teal)"
            trend={bytes24h > 0 ? 'incremental — deduped' : undefined}
          />
          <HealthScoreCard score={healthScore} />
        </div>

        {/* ── Row 2: Activity | Repo Health | Restore Verification ──────── */}
        <div style={s.row3}>
          <div className="dash-card" style={s.actCard}>
            <div style={s.cardHeader}>
              <span style={s.cardTitle}>Backup & Restore Activity</span>
              <div style={{ display:'flex', alignItems:'center', gap:8 }}>
                <ActivityLegend />
                {(['24h','7d','30d','1y'] as const).map(r => (
                  <button key={r} onClick={() => { setActRange(r); loadActivity(r) }}
                    style={{ ...s.rangeBtn, ...(actRange === r ? s.rangeBtnOn : {}) }}>
                    {r}
                  </button>
                ))}
              </div>
            </div>
            {activity.every(b => b.backups === 0 && b.restore_tests === 0 && b.failures === 0)
              ? <div style={s.actEmpty}>Activity history will appear as jobs and restore tests are collected.</div>
              : <>
                  <ActivityChart data={activity} height={160} />
                  <div style={s.actStats}>
                    <span>Backups: <strong style={{ color:'#38bdf8' }}>{activity.reduce((a,b)=>a+b.backups,0)}</strong></span>
                    <span>Restore Tests: <strong style={{ color:'var(--success)' }}>{activity.reduce((a,b)=>a+b.restore_tests,0)}</strong></span>
                    <span>Failures: <strong style={{ color:'var(--error)' }}>{activity.reduce((a,b)=>a+b.failures,0)}</strong></span>
                    <span style={{ marginLeft:'auto', color:'var(--accent-teal)', fontWeight:600 }}>
                      📦 {fmtBytes(activity.reduce((a,b)=>a+(b.bytes_added??0),0))} transferred
                    </span>
                  </div>
                </>
            }
          </div>
          <RepositoryHealthTable repos={repos} health={repoHealth} />
          <RestoreVerificationDonut snapshots={snapshots} restoreTests={restoreTests} />
        </div>

        {/* ── Row 3: Jobs | Agents | Alerts ─────────────────────────────── */}
        <div style={s.row3}>
          <RecentJobsTable jobs={recentJobs} systems={systemMap} />
          <AgentActivityCard systems={systems} />
          <AlertsPreview alerts={alerts} evidence={evidence} />
        </div>

      </div>
    </div>
  )
}

function fmtBytes(b: number): string {
  if (!b) return '—'
  if (b < 1024) return `${b} B`
  if (b < 1024 ** 2) return `${(b / 1024).toFixed(1)} KB`
  if (b < 1024 ** 3) return `${(b / 1024 ** 2).toFixed(1)} MB`
  return `${(b / 1024 ** 3).toFixed(2)} GB`
}

const s: Record<string, React.CSSProperties> = {
  page:     { display:'flex', flexDirection:'column', height:'100%', minHeight:0 },
  content:  { flex:1, overflowY:'auto', padding:'16px 20px', display:'flex', flexDirection:'column', gap:12 },
  kpiRow:   { display:'grid', gridTemplateColumns:'1fr 1fr 1fr 1fr 1fr 1.5fr', gap:10 },
  row3:     { display:'grid', gridTemplateColumns:'1fr 1fr 1fr', gap:12, alignItems:'start' },
  actCard:  {
    display:'flex', flexDirection:'column',
    background: 'linear-gradient(160deg, var(--bg-card-soft) 0%, var(--bg-card) 100%)',
    border: '1px solid var(--border)', borderRadius: 14,
    boxShadow: '0 4px 20px rgba(0,0,0,0.18)', overflow:'hidden',
  },
  cardHeader:{ display:'flex', justifyContent:'space-between', alignItems:'center', padding:'13px 18px 10px', borderBottom:'1px solid rgba(255,255,255,0.05)' },
  cardTitle: { fontSize:13, fontWeight:700, color:'var(--text)' },
  timeBadge: { fontSize:10, padding:'3px 8px', borderRadius:5, background:'rgba(255,255,255,0.05)', color:'var(--text-muted)', border:'1px solid var(--border)', fontWeight:600 },
  actStats:  { display:'flex', gap:20, padding:'10px 18px 14px', fontSize:12, color:'var(--text)', alignItems:'center', borderTop:'1px solid rgba(255,255,255,0.05)', marginTop:16 },
  rangeBtn:  { padding:'3px 8px', borderRadius:5, background:'rgba(255,255,255,0.04)', border:'1px solid var(--border)', color:'var(--text-muted)', fontSize:10, fontWeight:700, cursor:'pointer' },
  rangeBtnOn:{ background:'var(--accent-dim)', borderColor:'var(--accent)', color:'var(--accent)' },
  actEmpty:  { padding:'28px 20px', fontSize:12, color:'var(--text-dim)', textAlign:'center' as const, fontStyle:'italic' },
}
