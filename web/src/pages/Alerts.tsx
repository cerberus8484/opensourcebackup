import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'

const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080'

interface Alert {
  code:        string
  severity:    'critical' | 'warning' | 'info'
  category:    string
  title:       string
  description: string
  points:      number
  action:      string
}

interface AlertSummary {
  total:    number
  critical: number
  warning:  number
  info:     number
}

interface AlertsResponse {
  alerts:  Alert[]
  summary: AlertSummary
}

const SEV_COLOR: Record<string, string> = {
  critical: 'var(--error)',
  warning:  'var(--warning)',
  info:     'var(--text-muted)',
}
const SEV_BG: Record<string, string> = {
  critical: 'rgba(239,68,68,0.08)',
  warning:  'rgba(245,158,11,0.08)',
  info:     'rgba(255,255,255,0.03)',
}
const SEV_BORDER: Record<string, string> = {
  critical: 'rgba(239,68,68,0.25)',
  warning:  'rgba(245,158,11,0.25)',
  info:     'rgba(255,255,255,0.08)',
}
const SEV_ICON: Record<string, string> = {
  critical: '🔴',
  warning:  '⚠️',
  info:     'ℹ️',
}
const CAT_ICON: Record<string, string> = {
  backup:     '💾',
  restore:    '🔄',
  agent:      '⬡',
  repository: '▭',
  retention:  '⏰',
  system:     '⚙',
}

const CATEGORY_LABELS: Record<string, string> = {
  backup:     'Backup',
  restore:    'Restore',
  agent:      'Agent',
  repository: 'Repository',
  retention:  'Retention',
  system:     'System',
}

export function Alerts() {
  const navigate  = useNavigate()
  const [data,    setData]    = useState<AlertsResponse|null>(null)
  const [loading, setLoading] = useState(true)
  const [err,     setErr]     = useState<string|null>(null)
  const [catFilter, setCatFilter] = useState('all')
  const [sevFilter, setSevFilter] = useState('all')

  const load = () => {
    setLoading(true)
    fetch(`${BASE}/v1/health/alerts`)
      .then(r => r.ok ? r.json() : Promise.reject(r.status))
      .then(setData)
      .catch(() => setErr('Could not load alerts.'))
      .finally(() => setLoading(false))
  }

  useEffect(() => { queueMicrotask(load) }, [])

  const alerts = data?.alerts ?? []
  const summary = data?.summary ?? { total: 0, critical: 0, warning: 0, info: 0 }

  const categories = [...new Set(alerts.map(a => a.category))]
  const visible = alerts.filter(a =>
    (catFilter === 'all' || a.category === catFilter) &&
    (sevFilter === 'all' || a.severity === sevFilter)
  )

  return (
    <div style={s.page}>

      {/* Header */}
      <div style={s.header}>
        <div>
          <h1 style={s.h1}>Alerts</h1>
          <p style={s.sub}>
            Active operational alerts derived from the Backup Health Score.
            Alerts clear automatically when the underlying condition is resolved.
          </p>
        </div>
        <button onClick={load} style={s.refreshBtn} title="Refresh">↺ Refresh</button>
      </div>

      {/* Summary badges */}
      {!loading && !err && (
        <div style={s.summaryRow}>
          <SummaryBadge label="Total"    count={summary.total}    color="var(--text)" />
          <SummaryBadge label="Critical" count={summary.critical} color={SEV_COLOR.critical} />
          <SummaryBadge label="Warning"  count={summary.warning}  color={SEV_COLOR.warning} />
          <SummaryBadge label="Info"     count={summary.info}     color={SEV_COLOR.info} />
        </div>
      )}

      {/* All clear */}
      {!loading && !err && alerts.length === 0 && (
        <div style={s.allClear}>
          <div style={{ fontSize: 40, marginBottom: 12 }}>✅</div>
          <div style={{ fontSize: 18, fontWeight: 700, color: 'var(--success)', marginBottom: 4 }}>
            All checks passed
          </div>
          <div style={{ fontSize: 13, color: 'var(--text-muted)' }}>
            No active alerts. Your backup posture looks good.
          </div>
        </div>
      )}

      {/* Filters */}
      {alerts.length > 0 && (
        <div style={s.filters}>
          {/* Severity */}
          <div style={s.filterGroup}>
            {['all', 'critical', 'warning', 'info'].map(sev => (
              <button key={sev} onClick={() => setSevFilter(sev)}
                style={{ ...s.filterBtn, ...(sevFilter === sev ? s.filterBtnActive : {}),
                  color: sev === 'all' ? 'var(--text)' : SEV_COLOR[sev] }}>
                {sev === 'all' ? 'All Severity' : `${SEV_ICON[sev]} ${sev}`}
              </button>
            ))}
          </div>
          {/* Category */}
          <div style={s.filterGroup}>
            <button onClick={() => setCatFilter('all')}
              style={{ ...s.filterBtn, ...(catFilter === 'all' ? s.filterBtnActive : {}) }}>
              All Categories
            </button>
            {categories.map(cat => (
              <button key={cat} onClick={() => setCatFilter(cat)}
                style={{ ...s.filterBtn, ...(catFilter === cat ? s.filterBtnActive : {}) }}>
                {CAT_ICON[cat] ?? '?'} {CATEGORY_LABELS[cat] ?? cat}
              </button>
            ))}
          </div>
          <span style={{ fontSize: 12, color: 'var(--text-dim)', marginLeft: 'auto' }}>
            {visible.length} / {alerts.length} shown
          </span>
        </div>
      )}

      {/* Alert cards */}
      {loading && <div style={s.empty}>Loading…</div>}
      {err     && <div style={{ ...s.empty, color: 'var(--error)' }}>{err}</div>}

      <div style={s.cardList}>
        {visible.map(alert => (
          <div key={alert.code} style={{
            ...s.alertCard,
            background:   SEV_BG[alert.severity],
            borderColor:  SEV_BORDER[alert.severity],
          }}>
            <div style={s.alertTop}>
              <div style={s.alertLeft}>
                <span style={{ fontSize: 18 }}>{SEV_ICON[alert.severity]}</span>
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 3 }}>
                    <span style={{ ...s.alertTitle, color: SEV_COLOR[alert.severity] }}>
                      {alert.title}
                    </span>
                    <span style={s.catBadge}>
                      {CAT_ICON[alert.category]} {CATEGORY_LABELS[alert.category] ?? alert.category}
                    </span>
                    <span style={{ ...s.sevBadge, color: SEV_COLOR[alert.severity] }}>
                      −{alert.points} pts
                    </span>
                  </div>
                  <div style={s.alertDesc}>{alert.description}</div>
                </div>
              </div>
            </div>

            {alert.action && (
              <div style={s.actionRow}>
                <span style={s.actionLabel}>→ Action:</span>
                <span style={s.actionText}>{alert.action}</span>
              </div>
            )}

            {/* Quick navigation to relevant page */}
            {alert.category !== 'system' && (
              <div style={{ marginTop: 8 }}>
                <button
                  onClick={() => navigate(categoryRoute(alert.category))}
                  style={s.goBtn}
                >
                  Go to {CATEGORY_LABELS[alert.category]} →
                </button>
              </div>
            )}
          </div>
        ))}
      </div>

      {!loading && !err && alerts.length > 0 && (
        <div style={s.note}>
          Alerts are derived from the{' '}
          <button onClick={() => navigate('/')} style={s.inlineLink}>
            Backup Health Score
          </button>
          {' '}and refresh on every page load.
          Configure Prometheus alert rules in{' '}
          <code style={s.code}>deployments/prometheus/alert-rules.yml</code>.
        </div>
      )}

    </div>
  )
}

function SummaryBadge({ label, count, color }: { label: string; count: number; color: string }) {
  return (
    <div style={{ textAlign: 'center' as const, minWidth: 80 }}>
      <div style={{ fontSize: 28, fontWeight: 800, color, lineHeight: 1 }}>{count}</div>
      <div style={{ fontSize: 11, color: 'var(--text-dim)', marginTop: 2 }}>{label}</div>
    </div>
  )
}

function categoryRoute(cat: string): string {
  const routes: Record<string, string> = {
    backup:     '/jobs',
    restore:    '/restore-tests',
    agent:      '/agents',
    repository: '/repositories',
    retention:  '/policies',
  }
  return routes[cat] ?? '/'
}

const s: Record<string, React.CSSProperties> = {
  page:          { padding: '28px 36px', maxWidth: 960 },
  header:        { display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 20 },
  h1:            { fontSize: 22, fontWeight: 700, color: 'var(--text)', marginBottom: 4 },
  sub:           { fontSize: 13, color: 'var(--text-muted)', maxWidth: 600 },
  refreshBtn:    { padding: '7px 14px', borderRadius: 6, background: 'var(--bg-card)', border: '1px solid var(--border)', color: 'var(--text-muted)', fontSize: 12, cursor: 'pointer' },

  summaryRow:    { display: 'flex', gap: 28, marginBottom: 20, padding: '16px 20px', background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 10 },

  allClear:      { textAlign: 'center' as const, padding: '48px 32px', background: 'var(--bg-card)', border: '1px solid var(--border)', borderRadius: 10 },

  filters:       { display: 'flex', gap: 8, flexWrap: 'wrap' as const, alignItems: 'center', marginBottom: 16 },
  filterGroup:   { display: 'flex', gap: 4 },
  filterBtn:     { padding: '4px 10px', borderRadius: 6, background: 'var(--bg-card)', border: '1px solid var(--border)', fontSize: 12, cursor: 'pointer', color: 'var(--text-muted)' },
  filterBtnActive: { background: 'var(--accent-dim)', borderColor: 'var(--accent)', color: 'var(--text)' },

  empty:         { padding: 40, textAlign: 'center' as const, color: 'var(--text-dim)' },

  cardList:      { display: 'flex', flexDirection: 'column' as const, gap: 10 },
  alertCard:     { border: '1px solid', borderRadius: 10, padding: '14px 18px' },
  alertTop:      { display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' },
  alertLeft:     { display: 'flex', gap: 12, alignItems: 'flex-start' },
  alertTitle:    { fontSize: 15, fontWeight: 700 },
  alertDesc:     { fontSize: 13, color: 'var(--text-muted)' },
  catBadge:      { fontSize: 10, padding: '2px 7px', borderRadius: 4, background: 'rgba(255,255,255,0.06)', color: 'var(--text-dim)', fontWeight: 600 },
  sevBadge:      { fontSize: 11, fontWeight: 700 },
  actionRow:     { display: 'flex', gap: 6, marginTop: 10, padding: '8px 12px', background: 'rgba(255,255,255,0.03)', borderRadius: 6 },
  actionLabel:   { fontSize: 12, color: 'var(--text-dim)', fontWeight: 600, flexShrink: 0 },
  actionText:    { fontSize: 12, color: 'var(--text-muted)' },
  goBtn:         { padding: '4px 12px', borderRadius: 6, background: 'none', border: '1px solid var(--border)', color: 'var(--accent)', fontSize: 12, cursor: 'pointer' },
  note:          { marginTop: 16, fontSize: 12, color: 'var(--text-dim)', fontStyle: 'italic' },
  inlineLink:    { background: 'none', border: 'none', color: 'var(--accent)', fontSize: 12, cursor: 'pointer', padding: 0 },
  code:          { fontFamily: 'var(--font-mono)', fontSize: 11, background: 'rgba(0,0,0,0.2)', padding: '1px 5px', borderRadius: 3 },
}
