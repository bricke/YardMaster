// Every number and date formatter in the UI lives here. Unknown values show as "—",
// never as zero.

const nf = new Intl.NumberFormat()
const compact = new Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 })

export const DASH = '—'

export function num(v) {
  return v === null || v === undefined ? DASH : nf.format(v)
}

/** Token counts: compact above ten thousand (12.3K), exact below. */
export function tokens(v) {
  if (v === null || v === undefined) return DASH
  return Math.abs(v) >= 10000 ? compact.format(v) : nf.format(v)
}

export function pct(part, whole) {
  if (!whole) return DASH
  if (!part || part === whole) return part ? '100%' : '0%'
  return `${((part / whole) * 100).toFixed(1)}%`
}

/** Costs are estimates; null means unknown (an unpriced model), never free. */
export function cost(v, currency) {
  if (v === null || v === undefined) return DASH
  try {
    return new Intl.NumberFormat(undefined, {
      style: 'currency',
      currency: currency || 'USD',
      maximumFractionDigits: v < 1 ? 4 : 2,
    }).format(v)
  } catch {
    return `${v.toFixed(4)} ${currency}`
  }
}

export function ms(v) {
  if (v === null || v === undefined) return DASH
  if (v < 1000) return `${Math.round(v)} ms`
  return `${(v / 1000).toFixed(v < 10000 ? 2 : 1)} s`
}

/** Unix seconds to local date and time. */
export function dateTime(unix) {
  if (!unix) return DASH
  return new Date(unix * 1000).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
}

export function date(unix) {
  if (!unix) return DASH
  return new Date(unix * 1000).toLocaleDateString(undefined, { dateStyle: 'medium' })
}

/** "3 minutes ago" style, for last-used times. */
export function ago(unix) {
  if (!unix) return 'never'
  const s = Math.round(Date.now() / 1000 - unix)
  if (s < 60) return 'just now'
  const units = [
    [86400 * 365, 'year'],
    [86400 * 30, 'month'],
    [86400, 'day'],
    [3600, 'hour'],
    [60, 'minute'],
  ]
  for (const [size, name] of units) {
    if (s >= size) {
      const n = Math.floor(s / size)
      return `${n} ${name}${n === 1 ? '' : 's'} ago`
    }
  }
  return 'just now'
}

/** ISO timestamps (Go's time.Time) to local date and time. */
export function isoTime(v) {
  if (!v) return DASH
  return new Date(v).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' })
}
