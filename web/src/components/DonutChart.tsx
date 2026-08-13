import type { CSSProperties } from 'react'

interface Segment {
  value: number
  color: string
  label: string
}

interface Props {
  segments: Segment[]
  size?: number
  thickness?: number
  center?: React.ReactNode
}

/**
 * Lightweight SVG donut chart — no external library.
 * Renders segments proportionally; skips segments with value 0.
 */
export function DonutChart({ segments, size = 120, thickness = 18, center }: Props) {
  const r     = (size / 2) - thickness / 2
  const circ  = 2 * Math.PI * r
  const total = segments.reduce((a, s) => a + s.value, 0)

  if (total === 0) {
    return (
      <div style={{ ...container(size), position: 'relative' }}>
        <svg width={size} height={size}>
          <circle cx={size/2} cy={size/2} r={r}
            fill="none" stroke="var(--border)" strokeWidth={thickness} />
        </svg>
        {center && <div style={centerStyle()}>{center}</div>}
      </div>
    )
  }

  const gap   = total > 0 ? Math.min(circ * 0.01, 2) : 0
  const segmentsWithOffset = segments.filter(segment => segment.value > 0).reduce<Array<Segment & { offset: number }>>(
    (result, segment) => {
      const offset = result.length === 0
        ? 0
        : result[result.length - 1].offset + (result[result.length - 1].value / total) * circ
      result.push({ ...segment, offset })
      return result
    },
    [],
  )

  return (
    <div style={{ ...container(size), position: 'relative' }}>
      <svg width={size} height={size} style={{ transform: 'rotate(-90deg)' }}>
        {segmentsWithOffset.map((seg, i) => {
          const dash  = (seg.value / total) * circ - gap
          const space = circ - dash
          return (
            <circle key={i}
              cx={size/2} cy={size/2} r={r}
              fill="none"
              stroke={seg.color}
              strokeWidth={thickness}
              strokeDasharray={`${dash} ${space}`}
              strokeDashoffset={-seg.offset}
              strokeLinecap="butt"
            />
          )
        })}
      </svg>
      {center && <div style={centerStyle()}>{center}</div>}
    </div>
  )
}

function container(size: number): CSSProperties {
  return { width: size, height: size, flexShrink: 0 }
}

function centerStyle(): CSSProperties {
  return {
    position: 'absolute', inset: 0,
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    pointerEvents: 'none',
  }
}

interface LegendProps {
  segments: Segment[]
  total: number
}

/** Compact vertical legend for a donut chart. */
export function DonutLegend({ segments, total }: LegendProps) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6, justifyContent: 'center' }}>
      {segments.map((s, i) => (
        <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
          <span style={{
            width: 9, height: 9, borderRadius: '50%',
            background: s.color, flexShrink: 0,
          }} />
          <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>
            {s.label}
          </span>
          <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text)', marginLeft: 'auto', paddingLeft: 8 }}>
            {s.value}
            {total > 0 && (
              <span style={{ color: 'var(--text-dim)', fontWeight: 400, fontSize: 11 }}>
                {' '}({Math.round((s.value / total) * 100)}%)
              </span>
            )}
          </span>
        </div>
      ))}
    </div>
  )
}
