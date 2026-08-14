import { useEffect, useState } from 'react'
import { api, type BackupRepository, type ImmutableMode, type SecurityProvenance } from '../api'
import { Card, SectionHeader } from '../components/Card'
import { Table } from '../components/Table'
import { Modal } from '../components/Modal'
import { ConfirmDialog } from '../components/ConfirmDialog'

const TYPES = [
  { value: 'local',      label: 'Local Path',            hint: 'Local disk, USB drive, mounted volume on the agent system', icon: '💾' },
  { value: 'proxmox',    label: 'Proxmox Storage',       hint: 'Proxmox VE storage: /mnt/pve/Backup, /mnt/pve/NAS, etc.', icon: '🖥' },
  { value: 'nas-nfs',    label: 'NAS / NFS',             hint: 'Synology, QNAP, TrueNAS, Unraid via NFS mount', icon: '🗄' },
  { value: 'nas-smb',    label: 'NAS / SMB',             hint: 'Windows Share, Synology, QNAP via SMB/CIFS — e.g. Z:\\BackupFolder', icon: '🗄' },
  { value: 'minio-s3',   label: 'MinIO / S3',            hint: 'Self-hosted MinIO or AWS S3, GCS, Azure Blob, Backblaze B2', icon: '☁' },
  { value: 'restic',     label: 'Restic REST Server',    hint: 'Custom Restic REST server — any compatible backend', icon: '⚙' },
  { value: 'borg',       label: 'Borg (SSH)',            hint: 'Borg Backup via SSH — highly efficient deduplication', icon: '🔒' },
  { value: 'pgbackrest', label: 'pgBackRest',            hint: 'PostgreSQL only — WAL archiving, Point-in-Time Recovery', icon: '🐘' },
  { value: 'velero',     label: 'Velero (Kubernetes)',   hint: 'Kubernetes clusters — Deployments, Volumes, ConfigMaps', icon: '☸' },
]

const LOCATION_HINTS: Record<string, string> = {
  'local':      'C:\\Backups\\restic-repo  or  /var/backups/restic-repo',
  'proxmox':    '/mnt/pve/Backup/restic-repo  or  /mnt/pve/NAS/backups  (Proxmox storage path)',
  'nas-nfs':    '/mnt/nas-backup/restic-repo  (mount first: mount -t nfs nas:/volume1/backups /mnt/nas-backup)',
  'nas-smb':    'Z:\\OpenSourceBackup  (Windows)  or  /mnt/smb-backup/restic-repo  (Linux)',
  'minio-s3':   's3:http://minio.local:9000/my-bucket  or  s3:s3.amazonaws.com/my-bucket',
  'restic':     'rest:http://restic-server:8000/repo',
  'borg':       'ssh://user@host/./backups  or  user@nas-host:/volume1/borg-repo',
  'pgbackrest': 'path=/var/lib/pgbackrest  or  s3:my-pg-bucket/pgbackrest',
  'velero':     's3://my-bucket/velero  or  azure://container/velero',
}

const NAS_NOTE: Record<string, string> = {
  'proxmox':  'Proxmox storage is already mounted on the Proxmox host. The agent must run on the Proxmox host or a system with access to this path.',
  'nas-nfs':  'Mount the NFS share first on the agent system:\n  mount -t nfs nas-server:/volume1/backups /mnt/nas-backup',
  'nas-smb':  'Windows: Map as a network drive (e.g. Z:).\nLinux: mount -t cifs //nas-server/backups /mnt/smb -o user=backupuser',
}

export function Repositories() {
  const [repos,      setRepos]      = useState<BackupRepository[]>([])
  const [loading,    setLoading]    = useState(true)
  const [showForm,   setShowForm]   = useState(false)
  const [editingId,  setEditingId]  = useState<string|null>(null)
  const [deleteFor,  setDeleteFor]  = useState<BackupRepository|null>(null)
  const [saving,     setSaving]     = useState(false)
  const [err,        setErr]        = useState<string|null>(null)

  // form
  const [type,          setType]          = useState('restic')
  const [location,      setLocation]      = useState('')
  const [encryption,    setEncryption]    = useState('aes256')
  const [worm,          setWorm]          = useState(false)
  const [immutableMode, setImmutableMode] = useState<ImmutableMode>('none')

  const load = () => api.repositories().then(setRepos).finally(() => setLoading(false))
  useEffect(() => { load() }, [])

  function resetForm() {
    setType('restic'); setLocation(''); setEncryption('aes256'); setWorm(false)
    setImmutableMode('none'); setErr(null); setEditingId(null)
  }

  function openEdit(r: BackupRepository) {
    setEditingId(r.ID)
    setType(r.Type)
    setLocation(r.Location)
    setEncryption(r.EncryptionMode ?? 'aes256')
    setImmutableMode(r.ImmutableMode ?? 'none')
    setWorm(r.ObjectLockEnabled)
    setErr(null)
    setShowForm(true)
  }

  async function save() {
    if (!location.trim()) { setErr('Location is required.'); return }
    setSaving(true); setErr(null)
    try {
      const objLock = worm || immutableMode === 'object_lock'
      const data = {
        Type:              type,
        Location:          location.trim(),
        EncryptionMode:    encryption.trim() || undefined,
        ObjectLockEnabled: objLock,
        ImmutableMode:     immutableMode,
      }
      if (editingId) {
        await api.updateRepository(editingId, data)
      } else {
        await api.createRepository(data)
      }
      setShowForm(false); resetForm(); await load()
    } catch { setErr('Could not save repository. Check the location format.') }
    finally { setSaving(false) }
  }

  return (
    <div style={s.page}>
      <div style={s.topRow}>
        <SectionHeader title="Repositories" count={repos.length} />
        <button onClick={() => { resetForm(); setShowForm(true) }} style={s.newBtn}>
          + New Repository
        </button>
      </div>

      <p style={s.sub}>
        Repositories define <strong>where</strong> backups are stored.
        Every policy must reference a repository.
      </p>

      <Card>
        {loading ? <div style={s.load}>Loading…</div> : (
          <Table
            cols={[
              { header:'Backend',    render:r => <span style={s.badge}>{r.BackendType ?? 'UNKNOWN'}</span>, width:'110px' },
              { header:'Location',   render:r => <span style={s.mono}>{r.Location}</span> },
              { header:'Encryption', render:r => <SecurityBadge configured={Boolean(r.EncryptionMode)} provenance={r.SecurityPosture?.Encryption} label={r.EncryptionMode ?? 'Not configured'} />, width:'140px' },
              { header:'Immutability', render:r => <ImmutableBadge mode={r.ImmutableMode} provenance={r.SecurityPosture?.Immutability} />, width:'150px' },
              { header:'ID',         render:r => <span style={s.mono}>{r.ID.slice(0,8)}…</span> },
              { header:'Created',    render:r => new Date(r.CreatedAt).toLocaleDateString() },
              { header:'',           render:r => (
                  <div style={{display:'flex',gap:4}}>
                    <button onClick={() => openEdit(r)} style={s.editBtn}>✏</button>
                    <button onClick={() => setDeleteFor(r)} style={s.delBtn}>🗑</button>
                  </div>
                ), width:'70px' },
            ]}
            rows={repos} keyFn={r => r.ID}
            empty="No repositories yet. Click '+ New Repository' to add your first backup destination."
          />
        )}
      </Card>

      {/* ── New Repository Modal ── */}
      {showForm && (
        <Modal title={editingId ? 'Edit Repository' : 'Register New Repository'} onClose={() => { setShowForm(false); resetForm() }}>
          <div>
            <div style={s.field}>
              <label style={s.label}>Type <span style={s.req}>*</span></label>
              <div style={s.typeGrid}>
                {TYPES.map(t => (
                  <div key={t.value} onClick={() => setType(t.value)}
                    style={{...s.typeCard, ...(type===t.value ? s.typeCardOn : {})}}>
                    <span style={s.typeIcon}>{(t as {icon?:string}).icon ?? '📦'}</span>
                    <div style={s.typeName}>{t.label}</div>
                    <div style={s.typeHint}>{t.hint}</div>
                  </div>
                ))}
              </div>
            </div>

            <div style={s.field}>
              <label style={s.label}>Location / Path <span style={s.req}>*</span></label>
              <input style={s.input} value={location} onChange={e => setLocation(e.target.value)}
                placeholder={LOCATION_HINTS[type]} />
              <div style={s.hint2}>{LOCATION_HINTS[type]}</div>
              {NAS_NOTE[type] && (
                <div style={s.nasNote}>
                  {NAS_NOTE[type].split('\n').map((line, i) => (
                    <div key={i}>{i === 0 ? `ℹ ${line}` : `   ${line}`}</div>
                  ))}
                </div>
              )}
            </div>

            <div style={s.row2}>
              <div style={s.field}>
                <label style={s.label}>Encryption Mode</label>
                <input style={s.input} value={encryption} onChange={e => setEncryption(e.target.value)}
                  placeholder="aes256" />
                <div style={s.hint2}>Leave blank to disable encryption (not recommended)</div>
              </div>
              <div style={s.field}>
                <label style={s.label}>Write Protection (Immutability)</label>
                <select
                  style={{...s.input, cursor:'pointer'}}
                  value={immutableMode}
                  onChange={e => {
                    const m = e.target.value as ImmutableMode
                    setImmutableMode(m)
                    setWorm(m === 'object_lock' || m === 'worm')
                  }}
                >
                  <option value="none">None — no write protection</option>
                  <option value="object_lock">Object Lock — S3 / MinIO WORM</option>
                  <option value="worm">WORM — hardware / NAS write-once</option>
                  <option value="append_only">Append-Only — restic --append-only</option>
                  <option value="unknown">Unknown — not verified</option>
                </select>
                <div style={s.hint2}>
                  {immutableMode === 'none'        && 'No write protection. Snapshots can be deleted by anyone with repo access.'}
                  {immutableMode === 'object_lock' && 'S3 Object Lock prevents deletion for the retention period. Requires MinIO or AWS S3.'}
                  {immutableMode === 'worm'        && 'Hardware/NAS write-once: data cannot be overwritten or deleted.'}
                  {immutableMode === 'append_only' && 'Restic --append-only: agent cannot delete data, only the server can.'}
                  {immutableMode === 'unknown'     && 'Protection status has not been verified. Review storage configuration.'}
                </div>
              </div>
            </div>

            {err && <div style={s.errBox}>{err}</div>}
            <div style={s.actions}>
              <button onClick={() => { setShowForm(false); resetForm() }} style={s.cancelBtn}>Cancel</button>
              <button onClick={save} disabled={saving || !location.trim()} style={s.submitBtn}>
                {saving ? 'Saving…' : editingId ? '✓ Save Changes' : '✓ Register Repository'}
              </button>
            </div>
          </div>
        </Modal>
      )}

      {deleteFor && (
        <ConfirmDialog
          title={`Delete repository?`}
          message={`Delete "${deleteFor.Location}"? Policies linked to this repository will lose their backup destination.`}
          confirmLabel="Delete Repository" danger
          onConfirm={async () => { await api.deleteRepository(deleteFor.ID); setDeleteFor(null); await load() }}
          onCancel={() => setDeleteFor(null)}
        />
      )}
    </div>
  )
}

// ── ImmutableBadge ────────────────────────────────────────────────────────────

const IMMUTABLE_CONFIG: Record<string, { label: string; color: string; icon: string; tip: string }> = {
  object_lock:  { label: 'Object Lock', color: 'var(--success)',  icon: '🔒', tip: 'S3/MinIO WORM — deletion blocked at storage level' },
  worm:         { label: 'WORM',        color: 'var(--success)',  icon: '🔒', tip: 'Hardware/NAS write-once media' },
  append_only:  { label: 'Append-Only', color: '#22c55e',         icon: '📎', tip: 'Restic --append-only: agent cannot delete' },
  unknown:      { label: 'Unknown',     color: 'var(--warning)',  icon: '?',  tip: 'Write protection status not verified' },
  none:         { label: 'None',        color: 'var(--text-dim)', icon: '—',  tip: 'No write protection configured' },
}

function SecurityBadge({ configured, provenance, label }: { configured: boolean; provenance?: SecurityProvenance; label: string }) {
  const verified = configured && provenance === 'VERIFIED'
  return <span style={{ fontSize:12, color: verified ? 'var(--success)' : configured ? 'var(--warning)' : 'var(--text-dim)' }}>
    {label}{configured && ` (${verified ? 'verified' : 'declared'})`}
  </span>
}

function ImmutableBadge({ mode, provenance }: { mode?: string; provenance?: SecurityProvenance }) {
  const cfg = IMMUTABLE_CONFIG[mode ?? 'none'] ?? IMMUTABLE_CONFIG['none']
  return (
    <span title={cfg.tip} style={{
      display: 'inline-flex', alignItems: 'center', gap: 4,
      fontSize: 11, fontWeight: 600, color: cfg.color,
    }}>
      <span>{cfg.icon}</span>
      {cfg.label}{mode && mode !== 'none' && ` (${provenance === 'VERIFIED' ? 'verified' : 'declared'})`}
    </span>
  )
}

const s: Record<string, React.CSSProperties> = {
  page:       { padding:'28px 36px', maxWidth:1100 },
  topRow:     { display:'flex', justifyContent:'space-between', alignItems:'flex-start', marginBottom:6 },
  sub:        { fontSize:13, color:'var(--text-muted)', marginBottom:16 },
  load:       { padding:40, color:'var(--text-muted)', textAlign:'center' },
  newBtn:     { padding:'7px 16px', borderRadius:6, background:'var(--accent)', color:'#fff', border:'none', fontSize:13, fontWeight:600, cursor:'pointer' },
  mono:       { fontFamily:'var(--font-mono)', fontSize:12, color:'var(--accent)' },
  badge:      { display:'inline-block', padding:'2px 8px', borderRadius:4, background:'rgba(59,130,246,0.1)', color:'var(--accent)', fontSize:11, fontWeight:600 },
  enc:        { fontSize:12, color:'var(--success)' },
  worm:       { fontSize:12, color:'var(--success)' },
  dim:        { color:'var(--text-dim)', fontSize:12 },
  editBtn:    { padding:'3px 8px', borderRadius:5, background:'rgba(245,158,11,0.08)', color:'var(--warning)', border:'1px solid rgba(245,158,11,0.2)', fontSize:12, cursor:'pointer' },
  delBtn:     { padding:'3px 8px', borderRadius:5, background:'rgba(244,63,94,0.08)', color:'var(--error)', border:'1px solid rgba(244,63,94,0.2)', fontSize:12, cursor:'pointer' },
  field:      { marginBottom:16 },
  row2:       { display:'grid', gridTemplateColumns:'1fr 1fr', gap:14 },
  label:      { display:'block', fontSize:11, fontWeight:700, color:'var(--text-dim)', textTransform:'uppercase' as const, letterSpacing:'0.08em', marginBottom:6 },
  req:        { color:'var(--error)' },
  hint2:      { fontSize:11, color:'var(--text-dim)', marginTop:4 },
  input:      { width:'100%', padding:'8px 11px', background:'var(--bg)', border:'1px solid var(--border)', borderRadius:6, color:'var(--text)', fontSize:13, outline:'none' },
  typeGrid:   { display:'grid', gridTemplateColumns:'repeat(4,1fr)', gap:8 },
  typeCard:   { padding:'10px 12px', borderRadius:7, border:'1px solid var(--border)', cursor:'pointer', transition:'all 0.12s', textAlign:'center' as const },
  typeCardOn: { borderColor:'var(--accent)', background:'var(--accent-dim)' },
  typeIcon:   { fontSize:18, display:'block', marginBottom:4 },
  typeName:   { fontWeight:600, color:'var(--text)', fontSize:12, marginBottom:2 },
  typeHint:   { fontSize:10, color:'var(--text-dim)' },
  nasNote:    { marginTop:8, background:'rgba(59,130,246,0.07)', border:'1px solid rgba(59,130,246,0.2)', borderRadius:6, padding:'8px 12px', fontSize:11, color:'var(--text-muted)', fontFamily:'var(--font-mono)' },
  toggle:     { display:'flex', alignItems:'center', gap:10, padding:'8px 0', cursor:'pointer' },
  toggleDot:  { width:32, height:18, borderRadius:9, background:'var(--border)', display:'flex', alignItems:'center', justifyContent:'center', fontSize:10, color:'#fff', transition:'background 0.15s' },
  toggleOn:   { background:'var(--success)' },
  errBox:     { background:'rgba(244,63,94,0.1)', border:'1px solid rgba(244,63,94,0.25)', borderRadius:6, padding:'8px 12px', fontSize:13, color:'var(--error)', marginBottom:8 },
  actions:    { display:'flex', gap:8, justifyContent:'flex-end', paddingTop:16, borderTop:'1px solid var(--border)' },
  cancelBtn:  { padding:'7px 16px', borderRadius:6, background:'transparent', border:'1px solid var(--border)', color:'var(--text-muted)', fontSize:13, cursor:'pointer' },
  submitBtn:  { padding:'7px 20px', borderRadius:6, background:'var(--success)', color:'#000', border:'none', fontSize:13, fontWeight:700, cursor:'pointer' },
}
