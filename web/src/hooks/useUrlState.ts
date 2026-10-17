import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router'

/** 以網址查詢參數保存篩選條件（可書籤、可重新整理）。 */
export function useUrlState<T extends Record<string, string | undefined>>(defaults: T) {
  const [sp, setSp] = useSearchParams()
  const state = useMemo(() => {
    const out: Record<string, string | undefined> = { ...defaults }
    for (const k of Object.keys(defaults)) {
      const v = sp.get(k)
      if (v !== null) out[k] = v
    }
    return out as T
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sp])

  const update = useCallback(
    (patch: Partial<Record<keyof T, string | undefined | null>>) => {
      setSp(
        (prev) => {
          const next = new URLSearchParams(prev)
          for (const [k, v] of Object.entries(patch)) {
            if (v === undefined || v === null || v === '') next.delete(k)
            else next.set(k, String(v))
          }
          return next
        },
        { replace: true },
      )
    },
    [setSp],
  )
  return [state, update] as const
}
