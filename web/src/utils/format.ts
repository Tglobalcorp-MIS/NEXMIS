import dayjs from 'dayjs'

export function fmtTime(v: string | null | undefined, withSeconds = false): string {
  if (!v) return '-'
  const d = dayjs(v)
  if (!d.isValid() || d.year() < 1971) return '-'
  return d.format(withSeconds ? 'YYYY-MM-DD HH:mm:ss' : 'YYYY-MM-DD HH:mm')
}

export function fmtDate(v: string | null | undefined): string {
  if (!v) return '-'
  const d = dayjs(v)
  return d.isValid() ? d.format('YYYY-MM-DD') : v
}

export function fmtRelative(v: string | null | undefined): string {
  if (!v) return '-'
  const d = dayjs(v)
  if (!d.isValid() || d.year() < 1971) return '-'
  return d.fromNow()
}

export function fmtBytes(n: number | null | undefined, digits = 1): string {
  if (n === null || n === undefined || Number.isNaN(n)) return '-'
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB', 'PB']
  let v = n
  let i = -1
  do {
    v /= 1024
    i++
  } while (v >= 1024 && i < units.length - 1)
  return `${v.toFixed(digits)} ${units[i]}`
}

export function fmtPercent(v: number | null | undefined, digits = 0): string {
  if (v === null || v === undefined || Number.isNaN(v)) return '-'
  return `${v.toFixed(digits)}%`
}

export function fmtDuration(sec: number | null | undefined): string {
  if (sec === null || sec === undefined || sec < 0) return '-'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const parts: string[] = []
  if (d) parts.push(`${d} 天`)
  if (h) parts.push(`${h} 小時`)
  if (m || parts.length === 0) parts.push(`${m} 分`)
  return parts.join(' ')
}

export function fmtMhz(mhz: number | null | undefined): string {
  if (!mhz) return '-'
  return mhz >= 1000 ? `${(mhz / 1000).toFixed(2)} GHz` : `${mhz} MHz`
}

export function fmtSpeed(mbps: number | null | undefined): string {
  if (!mbps) return '-'
  return mbps >= 1000 ? `${(mbps / 1000).toFixed(mbps % 1000 ? 1 : 0)} Gbps` : `${mbps} Mbps`
}

export function durationBetween(a: string | null | undefined, b: string | null | undefined): string {
  if (!a || !b) return '-'
  const s = dayjs(b).diff(dayjs(a), 'second')
  if (s < 60) return `${s} 秒`
  return fmtDuration(s)
}

/** 剩餘天數（日期字串 → 今天起算）。 */
export function daysUntil(date: string | null | undefined): number | null {
  if (!date) return null
  const d = dayjs(date)
  if (!d.isValid()) return null
  return d.startOf('day').diff(dayjs().startOf('day'), 'day')
}

export function joinList(v: string[] | null | undefined, sep = ', '): string {
  return v && v.length ? v.join(sep) : '-'
}

export function percentColor(p: number): string {
  if (p >= 90) return '#ff4d4f'
  if (p >= 75) return '#faad14'
  return '#52c41a'
}
