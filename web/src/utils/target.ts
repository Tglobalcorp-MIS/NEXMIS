import type { Target } from '../api/types'

export const emptyTarget = (): Target => ({ all: false, device_ids: [], group_ids: [], tags: [] })

export function isTargetEmpty(t?: Target | null): boolean {
  if (!t) return true
  return !t.all && !t.device_ids?.length && !t.group_ids?.length && !t.tags?.length
}

export function targetValidator(_: unknown, v?: Target) {
  return isTargetEmpty(v) ? Promise.reject(new Error('請選擇目標')) : Promise.resolve()
}

export function describeTarget(t: Target | null | undefined, groupName?: (id: string) => string | undefined): string {
  if (!t) return '-'
  if (t.all) return '全部裝置'
  const parts: string[] = []
  if (t.device_ids?.length) parts.push(`${t.device_ids.length} 台裝置`)
  if (t.group_ids?.length) parts.push(`群組：${t.group_ids.map((g) => groupName?.(g) ?? g).join('、')}`)
  if (t.tags?.length) parts.push(`標籤：${t.tags.join('、')}`)
  return parts.join('；') || '-'
}
