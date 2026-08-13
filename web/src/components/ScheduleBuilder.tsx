import { useState, useEffect } from 'react'

// ── Types ─────────────────────────────────────────────────────────────────────

export interface ScheduleConfig {
  cron:              string
  timezone:          string
  window_start:      string   // HH:MM or ''
  window_end:        string
  if_missed:         'run_asap' | 'skip'
  restore_test_cron: string
  retention_cron:    string
}

// Shared form default; this module also exports the component below.
// eslint-disable-next-line react-refresh/only-export-components
export const EMPTY_SCHEDULE: ScheduleConfig = {
  cron: '', timezone: 'Europe/Berlin',
  window_start: '', window_end: '',
  if_missed: 'run_asap',
  restore_test_cron: '', retention_cron: '',
}

type ScheduleMode = 'none' | 'daily' | 'weekly' | 'monthly' | 'custom'

const DAYS = ['Mon','Tue','Wed','Thu','Fri','Sat','Sun']
const DAY_CRON = ['1','2','3','4','5','6','0']

const TIMEZONES = [
  'UTC','Europe/Berlin','Europe/Vienna','Europe/Zurich','Europe/London',
  'America/New_York','America/Chicago','America/Los_Angeles',
  'Asia/Tokyo','Asia/Shanghai','Australia/Sydney',
]

// ── Cron helpers ──────────────────────────────────────────────────────────────

function parseCron(cron: string): { mode: ScheduleMode; time: string; days: string[]; dom: string } {
  if (!cron) return { mode: 'none', time: '02:00', days: [], dom: '1' }
  const p = cron.trim().split(/\s+/)
  if (p.length !== 5) return { mode: 'custom', time: '', days: [], dom: '' }
  const [min, hour, dom, , dow] = p
  const time = `${hour.padStart(2,'0')}:${min.padStart(2,'0')}`
  if (dom === '*' && dow === '*') return { mode: 'daily', time, days: [], dom: '' }
  if (dom === '*' && dow !== '*') {
    const days = dow.split(',').map(d => DAY_CRON.indexOf(d)).filter(i => i >= 0).map(i => DAYS[i])
    return { mode: 'weekly', time, days, dom: '' }
  }
  if (dom !== '*' && dow === '*') return { mode: 'monthly', time, days: [], dom }
  return { mode: 'custom', time, days: [], dom: '' }
}

function buildCron(mode: ScheduleMode, time: string, days: string[], dom: string): string {
  if (mode === 'none') return ''
  const [h, m] = (time || '02:00').split(':')
  const hour = (h || '2'), min = (m || '0')
  if (mode === 'daily')   return `${min} ${hour} * * *`
  if (mode === 'weekly') {
    const dow = days.map(d => DAY_CRON[DAYS.indexOf(d)]).join(',') || '1'
    return `${min} ${hour} * * ${dow}`
  }
  if (mode === 'monthly') return `${min} ${hour} ${dom || '1'} * *`
  return time // custom: user enters raw cron
}

function nextRun(cron: string, tz: string): string {
  if (!cron) return '—'
  try {
    // Simple human-readable approximation (no full cron parser)
    const parsed = parseCron(cron)
    if (parsed.mode === 'daily')   return `Daily at ${parsed.time} (${tz})`
    if (parsed.mode === 'weekly')  return `Weekly ${parsed.days.join('/')} at ${parsed.time} (${tz})`
    if (parsed.mode === 'monthly') return `Monthly day ${parsed.dom} at ${parsed.time} (${tz})`
    return `Cron: ${cron}`
  } catch { return cron }
}

// ── Sub-component: single schedule row ───────────────────────────────────────

interface ScheduleRowProps {
  label:       string
  hint:        string
  value:       string
  onChange:    (cron: string) => void
  timezone:    string
  defaultTime: string
}

function ScheduleRow({ label, hint, value, onChange, timezone, defaultTime }: ScheduleRowProps) {
  const parsed = parseCron(value)
  const [mode, setMode]   = useState<ScheduleMode>(parsed.mode)
  const [time, setTime]   = useState(parsed.time || defaultTime)
  const [days, setDays]   = useState<string[]>(parsed.days.length ? parsed.days : ['Mon'])
  const [dom,  setDom]    = useState(parsed.dom || '1')
  const [raw,  setRaw]    = useState(value)

  useEffect(() => {
    if (mode === 'custom') {
      onChange(raw)
    } else {
      onChange(buildCron(mode, time, days, dom))
    }
  }, [mode, time, days, dom, raw]) // eslint-disable-line

  const toggleDay = (d: string) => {
    const next = days.includes(d) ? days.filter(x => x !== d) : [...days, d]
    if (next.length > 0) setDays(next)
  }

  return (
    <div style={rs.wrap}>
      <div style={rs.header}>
        <span style={rs.label}>{label}</span>
        <span style={rs.hint}>{hint}</span>
      </div>

      {/* Mode selector */}
      <div style={rs.modeRow}>
        {(['none','daily','weekly','monthly','custom'] as ScheduleMode[]).map(m => (
          <button key={m} onClick={() => setMode(m)}
            style={{ ...rs.modeBtn, ...(mode === m ? rs.modeBtnOn : {}) }}>
            {m === 'none' ? 'Manual' : m.charAt(0).toUpperCase() + m.slice(1)}
          </button>
        ))}
      </div>

      {/* Config */}
      {mode !== 'none' && mode !== 'custom' && (
        <div style={rs.config}>
          <div style={rs.configRow}>
            <label style={rs.subLabel}>Time</label>
            <input type="time" value={time} onChange={e => setTime(e.target.value)} style={rs.timeInput} />
          </div>

          {mode === 'weekly' && (
            <div style={rs.configRow}>
              <label style={rs.subLabel}>Days</label>
              <div style={{ display: 'flex', gap: 4 }}>
                {DAYS.map(d => (
                  <button key={d} onClick={() => toggleDay(d)}
                    style={{ ...rs.dayBtn, ...(days.includes(d) ? rs.dayBtnOn : {}) }}>
                    {d}
                  </button>
                ))}
              </div>
            </div>
          )}

          {mode === 'monthly' && (
            <div style={rs.configRow}>
              <label style={rs.subLabel}>Day of month</label>
              <input type="number" min={1} max={28} value={dom}
                onChange={e => setDom(e.target.value)}
                style={{ ...rs.timeInput, width: 60 }} />
            </div>
          )}
        </div>
      )}

      {mode === 'custom' && (
        <div style={rs.config}>
          <label style={rs.subLabel}>Cron expression</label>
          <input value={raw} onChange={e => setRaw(e.target.value)}
            placeholder="0 2 * * *" style={rs.cronInput} />
          <div style={rs.hint}>min hour dom month dow — e.g. "0 2 * * *" = daily at 02:00</div>
        </div>
      )}

      {/* Preview */}
      {mode !== 'none' && (
        <div style={rs.preview}>
          ↻ {nextRun(mode === 'custom' ? raw : buildCron(mode, time, days, dom), timezone)}
        </div>
      )}
    </div>
  )
}

// ── Main ScheduleBuilder ──────────────────────────────────────────────────────

interface Props {
  value:    ScheduleConfig
  onChange: (cfg: ScheduleConfig) => void
}

export function ScheduleBuilder({ value, onChange }: Props) {
  const set = (patch: Partial<ScheduleConfig>) => onChange({ ...value, ...patch })

  return (
    <div style={s.wrap}>

      {/* Timezone */}
      <div style={s.field}>
        <label style={s.label}>Timezone</label>
        <select value={value.timezone} onChange={e => set({ timezone: e.target.value })} style={s.select}>
          {TIMEZONES.map(tz => <option key={tz} value={tz}>{tz}</option>)}
        </select>
      </div>

      {/* Backup schedule */}
      <ScheduleRow
        label="Backup Schedule"
        hint="When should backups run automatically?"
        value={value.cron}
        onChange={cron => set({ cron })}
        timezone={value.timezone}
        defaultTime="02:00"
      />

      {/* Backup window */}
      <div style={s.field}>
        <label style={s.label}>Backup Window <span style={s.opt}>(optional)</span></label>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={s.subLabel}>From</span>
          <input type="time" value={value.window_start}
            onChange={e => set({ window_start: e.target.value })} style={s.timeSmall} />
          <span style={s.subLabel}>Until</span>
          <input type="time" value={value.window_end}
            onChange={e => set({ window_end: e.target.value })} style={s.timeSmall} />
          {(value.window_start || value.window_end) && (
            <button onClick={() => set({ window_start: '', window_end: '' })} style={s.clearBtn}>✕ Clear</button>
          )}
        </div>
        <div style={s.hint}>Backups only start within this time window. Leave empty for no restriction.</div>
      </div>

      {/* If missed */}
      <div style={s.field}>
        <label style={s.label}>If schedule is missed</label>
        <div style={{ display: 'flex', gap: 8 }}>
          {[
            { v: 'run_asap', l: 'Run as soon as possible' },
            { v: 'skip',     l: 'Skip until next schedule' },
          ].map(({ v, l }) => (
            <button key={v} onClick={() => set({ if_missed: v as 'run_asap'|'skip' })}
              style={{ ...s.optBtn, ...(value.if_missed === v ? s.optBtnOn : {}) }}>
              {l}
            </button>
          ))}
        </div>
      </div>

      <div style={s.divider} />

      {/* Restore test schedule */}
      <ScheduleRow
        label="Restore Test Schedule"
        hint="Run restore tests automatically to verify recoverability."
        value={value.restore_test_cron}
        onChange={restore_test_cron => set({ restore_test_cron })}
        timezone={value.timezone}
        defaultTime="10:00"
      />

      <div style={s.divider} />

      {/* Retention/prune schedule */}
      <ScheduleRow
        label="Retention / Prune Schedule"
        hint="When should old snapshots be removed according to keep rules?"
        value={value.retention_cron}
        onChange={retention_cron => set({ retention_cron })}
        timezone={value.timezone}
        defaultTime="04:00"
      />

    </div>
  )
}

// ── Styles ─────────────────────────────────────────────────────────────────────

const s: Record<string, React.CSSProperties> = {
  wrap:      { display: 'flex', flexDirection: 'column', gap: 20 },
  field:     { display: 'flex', flexDirection: 'column', gap: 6 },
  label:     { fontSize: 11, fontWeight: 700, color: 'var(--text-dim)', textTransform: 'uppercase', letterSpacing: '0.08em' },
  subLabel:  { fontSize: 12, color: 'var(--text-muted)', minWidth: 40 },
  opt:       { fontWeight: 400, textTransform: 'none', letterSpacing: 0, color: 'var(--text-dim)', fontSize: 10 },
  hint:      { fontSize: 11, color: 'var(--text-dim)', marginTop: 2 },
  select:    { padding: '7px 10px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 13, outline: 'none', maxWidth: 200 },
  timeSmall: { padding: '5px 8px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 13, outline: 'none', width: 100 },
  clearBtn:  { padding: '4px 10px', borderRadius: 6, background: 'none', border: '1px solid var(--border)', color: 'var(--text-dim)', fontSize: 11, cursor: 'pointer' },
  optBtn:    { padding: '6px 14px', borderRadius: 6, background: 'var(--bg)', border: '1px solid var(--border)', color: 'var(--text-muted)', fontSize: 12, cursor: 'pointer' },
  optBtnOn:  { background: 'var(--accent-dim)', borderColor: 'var(--accent)', color: 'var(--text)', fontWeight: 600 },
  divider:   { height: 1, background: 'var(--border)' },
}

const rs: Record<string, React.CSSProperties> = {
  wrap:      { background: 'rgba(255,255,255,0.02)', borderRadius: 8, padding: '12px 16px', border: '1px solid var(--border)' },
  header:    { display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 10 },
  label:     { fontSize: 13, fontWeight: 700, color: 'var(--text)' },
  hint:      { fontSize: 11, color: 'var(--text-dim)' },
  modeRow:   { display: 'flex', gap: 4, marginBottom: 12 },
  modeBtn:   { padding: '4px 12px', borderRadius: 6, background: 'var(--bg)', border: '1px solid var(--border)', color: 'var(--text-muted)', fontSize: 12, cursor: 'pointer' },
  modeBtnOn: { background: 'var(--accent-dim)', borderColor: 'var(--accent)', color: 'var(--text)', fontWeight: 600 },
  config:    { display: 'flex', flexDirection: 'column', gap: 8 },
  configRow: { display: 'flex', alignItems: 'center', gap: 10 },
  subLabel:  { fontSize: 12, color: 'var(--text-dim)', minWidth: 60 },
  timeInput: { padding: '5px 8px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 13, outline: 'none', width: 110 },
  cronInput: { padding: '6px 10px', background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 6, color: 'var(--text)', fontSize: 13, fontFamily: 'var(--font-mono)', outline: 'none', width: '100%' },
  dayBtn:    { padding: '3px 8px', borderRadius: 5, background: 'var(--bg)', border: '1px solid var(--border)', color: 'var(--text-muted)', fontSize: 11, cursor: 'pointer', fontWeight: 600 },
  dayBtnOn:  { background: 'var(--accent)', borderColor: 'var(--accent)', color: '#fff' },
  preview:   { marginTop: 8, fontSize: 11, color: 'var(--accent)', fontStyle: 'italic' },
}
