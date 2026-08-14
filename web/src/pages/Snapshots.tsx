import { useEffect, useState } from 'react'
import { api, fmt, timeAgo, type BackupJob, type Snapshot } from '../api'
import { Card, SectionHeader } from '../components/Card'
import { StatusBadge } from '../components/StatusBadge'
import { Table } from '../components/Table'

export function Snapshots() {
  const [snapshots, setSnapshots] = useState<Snapshot[]>([])
  const [jobs,      setJobs]      = useState<BackupJob[]>([])
  const [loading,   setLoading]   = useState(true)

  useEffect(() => {
    Promise.all([api.snapshots(), api.jobs()])
      .then(([sn,j]) => { setSnapshots(sn); setJobs(j) })
      .finally(()=>setLoading(false))
  }, [])

  const sorted = [...snapshots].sort((a,b)=>new Date(b.CreatedAt).getTime()-new Date(a.CreatedAt).getTime())

  return (
    <div style={s.page}>
      <SectionHeader title="Snapshots" count={snapshots.length} />

      <div style={s.notice}>
        Snapshots without a restore test are not necessarily bad — but they are unverified.
        A backup is only proven when a restore succeeds.
      </div>

      <Card>
        {loading ? <div style={s.load}>Loading…</div> : (
          <Table
            cols={[
              { header:'Snapshot ID',  render:sn=><span style={s.mono}>{sn.EngineSnapshotID.slice(0,14)}…</span> },
              { header:'Restore Test', render:()=><StatusBadge status="not tested" />, width:'120px' },
              { header:'Checksum',     render:sn=><StatusBadge status={sn.ChecksumStatus} />, width:'110px' },
              { header:'Size',         render:sn=>{ const j=jobs.find(j=>j.ID===sn.JobID); return fmt(j?.BytesUploaded) }},
              { header:'Paths',        render:sn=>(
                <div>{(sn.Paths??[]).map(p=><div key={p} style={s.path}>{p}</div>)}</div>
              )},
              { header:'Repository',   render:sn=><span style={s.mono}>{sn.RepositoryID.slice(0,8)}…</span> },
              { header:'Created',      render:sn=>timeAgo(sn.CreatedAt) },
            ]}
            rows={sorted} keyFn={sn=>sn.ID}
            empty="No snapshots yet. Run a backup job to create your first snapshot."
          />
        )}
      </Card>
    </div>
  )
}

const s: Record<string,React.CSSProperties> = {
  page:   { padding:'28px 36px', maxWidth:1200 },
  load:   { padding:40, color:'var(--text-muted)', textAlign:'center' },
  notice: { background:'rgba(59,130,246,0.06)', border:'1px solid rgba(59,130,246,0.15)', borderRadius:'var(--radius)', padding:'10px 16px', fontSize:13, color:'var(--text-muted)', marginBottom:14 },
  mono:   { fontFamily:'var(--font-mono)', fontSize:12, color:'var(--accent)' },
  path:   { fontFamily:'var(--font-mono)', fontSize:11, color:'var(--text-muted)' },
}
