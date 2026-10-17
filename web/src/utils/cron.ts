// 標準 5 欄位 cron（分 時 日 月 週）的驗證與中文說明。

export const cronTemplates: { label: string; value: string }[] = [
  { label: '每 15 分鐘', value: '*/15 * * * *' },
  { label: '每小時整點', value: '0 * * * *' },
  { label: '每天 03:00', value: '0 3 * * *' },
  { label: '每天 12:30', value: '30 12 * * *' },
  { label: '每個工作日 08:00', value: '0 8 * * 1-5' },
  { label: '每週一 03:00', value: '0 3 * * 1' },
  { label: '每週五 18:00', value: '0 18 * * 5' },
  { label: '每月 1 日 02:00', value: '0 2 1 * *' },
]

const ranges: [number, number][] = [
  [0, 59],
  [0, 23],
  [1, 31],
  [1, 12],
  [0, 7],
]
const fieldNames = ['分', '時', '日', '月', '週']
const weekNames = ['日', '一', '二', '三', '四', '五', '六', '日']

function validField(f: string, [min, max]: [number, number]): boolean {
  return f.split(',').every((part) => {
    const [base, step] = part.split('/')
    if (step !== undefined && !/^\d+$/.test(step)) return false
    if (base === '*') return true
    const m = /^(\d+)(?:-(\d+))?$/.exec(base)
    if (!m) return false
    const a = Number(m[1])
    const b = m[2] !== undefined ? Number(m[2]) : a
    return a >= min && b <= max && a <= b
  })
}

export function validateCron(expr: string): string | null {
  const parts = expr.trim().split(/\s+/)
  if (parts.length !== 5) return '需為 5 個欄位：分 時 日 月 週'
  for (let i = 0; i < 5; i++) {
    if (!validField(parts[i], ranges[i])) return `「${fieldNames[i]}」欄位格式錯誤：${parts[i]}`
  }
  return null
}

const pad = (n: string) => n.padStart(2, '0')

function describeList(f: string, unit: (v: string) => string): string {
  return f
    .split(',')
    .map((p) => {
      const [base, step] = p.split('/')
      const range = base === '*' ? '' : base.includes('-') ? base.split('-').map(unit).join(' 至 ') : unit(base)
      if (step) return `${range ? range + ' 間' : ''}每 ${step}`
      return range
    })
    .join('、')
}

export function describeCron(expr: string): string {
  if (validateCron(expr)) return '無效的排程運算式'
  const [mi, h, dom, mon, dow] = expr.trim().split(/\s+/)
  const segs: string[] = []

  if (mon !== '*') segs.push(describeList(mon, (v) => `${v} 月`))
  if (dom !== '*') segs.push(`${describeList(dom, (v) => `${v} 日`)}${dom.includes('/') ? ' 天' : ''}`)
  if (dow !== '*') {
    if (dow === '1-5') segs.push('每個工作日')
    else segs.push(`每週${describeList(dow, (v) => weekNames[Number(v)] ?? v)}`)
  }
  if (segs.length === 0) segs.push('每天')

  let time: string
  const simple = (s: string) => /^\d+$/.test(s)
  if (simple(mi) && simple(h)) time = `${pad(h)}:${pad(mi)}`
  else if (simple(mi) && h === '*') time = mi === '0' ? '每小時整點' : `每小時第 ${mi} 分`
  else if (mi.startsWith('*/') && h === '*') time = `每 ${mi.slice(2)} 分鐘`
  else if (simple(mi) && h.startsWith('*/')) time = `每 ${h.slice(2)} 小時（第 ${mi} 分）`
  else if (simple(mi)) time = `${describeList(h, (v) => `${v} 時`)} 的第 ${mi} 分`
  else time = `${describeList(h, (v) => `${v} 時`) || '每小時'}，${describeList(mi, (v) => `${v} 分`) || '每分鐘'}`

  if (segs[0] === '每天' && (time.startsWith('每') || time.startsWith('每小時'))) return time
  return `${segs.join('，')} ${time}`
}
