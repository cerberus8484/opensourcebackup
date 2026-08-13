// Confirmation dialog for stopping a running backup (B_JOB_CANCEL).
// Behaviour unchanged from the original inline dialog — extracted for clarity.
import type { BackupJob, BackupPolicy, System } from '../../api'

export interface CancelTarget {
  job:    BackupJob
  system: System
  policy: BackupPolicy | null
}

// Shared dialog options; this module also exports the component below.
// eslint-disable-next-line react-refresh/only-export-components
export const CANCEL_REASONS = ['Windows-Update', 'Maschine instabil', 'Arbeitsbetrieb', 'Sonstiges']

interface CancelDialogProps {
  target:    CancelTarget
  reason:    string
  cancelling: boolean
  onReason:  (r: string) => void
  onDismiss: () => void
  onConfirm: () => void
}

export function CancelDialog({ target, reason, cancelling, onReason, onDismiss, onConfirm }: CancelDialogProps) {
  return (
    <div style={s.overlay}>
      <div style={s.dialog}>
        <div style={s.head}><span style={s.title}>⏹ Backup stoppen</span></div>
        <div style={s.body}>
          <div style={s.row}><span style={s.key}>System</span><span style={s.val}>{target.system.Hostname}</span></div>
          {target.policy && (
            <div style={s.row}><span style={s.key}>Policy</span><span style={s.val}>{target.policy.Name}</span></div>
          )}
          <div style={{ marginTop: 18 }}>
            <div style={s.label}>Grund</div>
            <div style={s.grid}>
              {CANCEL_REASONS.map(r => (
                <button key={r} onClick={() => onReason(r)} style={{ ...s.reason, ...(reason === r ? s.reasonOn : {}) }}>
                  {r}
                </button>
              ))}
            </div>
          </div>
          <div style={s.note}>
            Stop = kontrollierter Abbruch. Der Job erhält Status „cancelled" — kein Fehler.
            Der nächste geplante Lauf startet normal.
          </div>
        </div>
        <div style={s.foot}>
          <button onClick={onDismiss} disabled={cancelling} style={s.dismiss}>Abbrechen</button>
          <button onClick={onConfirm} disabled={cancelling} style={s.stop}>
            {cancelling ? 'Stoppe…' : '⏹ Backup stoppen'}
          </button>
        </div>
      </div>
    </div>
  )
}

const s: Record<string, React.CSSProperties> = {
  overlay:  { position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.65)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000 },
  dialog:   { width: 440, maxWidth: '92vw', background: 'linear-gradient(180deg, rgba(21,28,46,0.99) 0%, rgba(10,15,27,0.99) 100%)', border: '1px solid var(--border)', borderRadius: 16, overflow: 'hidden' },
  head:     { padding: '18px 20px', borderBottom: '1px solid var(--border)', background: 'rgba(239,68,68,0.06)' },
  title:    { fontSize: 14, fontWeight: 700, color: 'var(--error)', letterSpacing: '0.04em' },
  body:     { padding: 20 },
  row:      { display: 'flex', gap: 12, alignItems: 'center', marginBottom: 8 },
  key:      { fontSize: 11, fontWeight: 700, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.08em', width: 52, flexShrink: 0 },
  val:      { fontSize: 13, color: 'var(--text)', fontWeight: 600 },
  label:    { fontSize: 10, fontWeight: 700, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.1em' },
  grid:     { display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, marginTop: 8 },
  reason:   { padding: '9px 12px', borderRadius: 10, background: 'rgba(255,255,255,0.04)', border: '1px solid var(--border)', color: 'var(--text-muted)', fontSize: 12, fontWeight: 500, cursor: 'pointer', textAlign: 'left' },
  reasonOn: { background: 'rgba(239,68,68,0.12)', border: '1px solid rgba(239,68,68,0.35)', color: 'var(--error)', fontWeight: 700 },
  note:     { marginTop: 16, fontSize: 11, color: 'var(--text-dim)', lineHeight: 1.6, padding: '10px 12px', background: 'rgba(255,255,255,0.02)', borderRadius: 8 },
  foot:     { display: 'flex', justifyContent: 'flex-end', gap: 10, padding: '16px 20px', borderTop: '1px solid var(--border)' },
  dismiss:  { padding: '9px 18px', borderRadius: 10, background: 'transparent', border: '1px solid var(--border)', color: 'var(--text-muted)', fontSize: 13, cursor: 'pointer' },
  stop:     { padding: '9px 22px', borderRadius: 10, background: 'var(--error)', color: '#fff', border: 'none', fontSize: 13, fontWeight: 700, cursor: 'pointer' },
}
