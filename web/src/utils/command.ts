import type { CommandKind, NewCommand } from '../api/types'

/** 指令編輯表單的值（扁平化後再轉成 NewCommand）。 */
export interface CommandFormValues {
  kind: CommandKind
  script?: string
  timeout_sec?: number
  countdown_sec?: number
  message?: string
  allow_defer?: boolean
  force?: boolean
  notify_title?: string
  notify_message?: string
  notify_level?: 'info' | 'warning' | 'critical'
  service_name?: string
  process_name?: string
  process_pid?: number
}

export const editableKinds: CommandKind[] = [
  'powershell',
  'cmd',
  'refresh',
  'reboot',
  'shutdown',
  'lock',
  'logoff',
  'notify',
  'service.restart',
  'process.kill',
]

/** helpdesk 只能派送的指令種類 */
export const helpdeskKinds: CommandKind[] = ['refresh', 'notify', 'lock']

export const defaultCommandValues: CommandFormValues = {
  kind: 'powershell',
  script: '',
  timeout_sec: 300,
  countdown_sec: 300,
  allow_defer: true,
  force: false,
  notify_level: 'info',
}

export function toNewCommand(v: CommandFormValues): NewCommand {
  const timeout_sec = v.timeout_sec ?? 300
  switch (v.kind) {
    case 'powershell':
    case 'cmd':
      return { kind: v.kind, script: v.script ?? '', timeout_sec }
    case 'reboot':
    case 'shutdown':
      return {
        kind: v.kind,
        timeout_sec,
        args: {
          force: !!v.force,
          countdown_sec: v.countdown_sec ?? 300,
          message: v.message ?? '',
          allow_defer: !!v.allow_defer,
        },
      }
    case 'notify':
      return {
        kind: 'notify',
        timeout_sec,
        args: { title: v.notify_title ?? '', message: v.notify_message ?? '', level: v.notify_level ?? 'info' },
      }
    case 'service.restart':
      return { kind: v.kind, timeout_sec, args: { name: v.service_name ?? '' } }
    case 'process.kill':
      return {
        kind: v.kind,
        timeout_sec,
        args: v.process_pid ? { pid: v.process_pid } : { name: v.process_name ?? '' },
      }
    default:
      return { kind: v.kind, timeout_sec }
  }
}

export function fromNewCommand(c: NewCommand | undefined): CommandFormValues {
  if (!c) return { ...defaultCommandValues }
  const a = (c.args ?? {}) as Record<string, unknown>
  const s = (k: string) => (typeof a[k] === 'string' ? (a[k] as string) : undefined)
  const n = (k: string) => (typeof a[k] === 'number' ? (a[k] as number) : undefined)
  const b = (k: string) => (typeof a[k] === 'boolean' ? (a[k] as boolean) : undefined)
  return {
    ...defaultCommandValues,
    kind: c.kind,
    script: c.script ?? '',
    timeout_sec: c.timeout_sec ?? 300,
    countdown_sec: n('countdown_sec') ?? 300,
    message: s('message'),
    allow_defer: b('allow_defer') ?? true,
    force: b('force') ?? false,
    notify_title: s('title'),
    notify_message: c.kind === 'notify' ? s('message') : undefined,
    notify_level: (s('level') as CommandFormValues['notify_level']) ?? 'info',
    service_name: c.kind === 'service.restart' ? s('name') : undefined,
    process_name: c.kind === 'process.kill' ? s('name') : undefined,
    process_pid: n('pid'),
  }
}


/** 指令內容的單行摘要（表格顯示用）。 */
export function commandSummary(c: { kind: CommandKind; args: Record<string, unknown> | null }): string {
  const a = c.args ?? {}
  const str = (k: string) => (typeof a[k] === 'string' ? (a[k] as string) : '')
  switch (c.kind) {
    case 'powershell':
    case 'cmd':
      return (str('script') || '').split('\n')[0]
    case 'reboot':
    case 'shutdown':
      return `倒數 ${String(a.countdown_sec ?? 0)} 秒${a.force ? '，強制' : ''}${a.allow_defer ? '，可延後' : ''}`
    case 'notify':
      return `${str('title')}：${str('message')}`
    case 'service.restart':
      return str('name')
    case 'process.kill':
      return str('name') || (a.pid ? `PID ${String(a.pid)}` : '')
    case 'uninstall':
    case 'install':
      return str('name') || str('file_name')
    default:
      return ''
  }
}
