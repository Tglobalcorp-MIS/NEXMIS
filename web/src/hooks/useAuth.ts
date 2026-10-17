import { useCallback } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { authApi } from '../api/endpoints'
import { qk } from '../api/keys'
import type { Role, User } from '../api/types'

export const roleRank: Record<Role, number> = { viewer: 0, helpdesk: 1, operator: 2, admin: 3 }

export function hasRole(user: User | null | undefined, min: Role): boolean {
  if (!user) return false
  return (roleRank[user.role] ?? -1) >= roleRank[min]
}

export function useMe() {
  return useQuery({
    queryKey: qk.me,
    queryFn: authApi.me,
    retry: false,
    staleTime: 5 * 60_000,
    meta: { silent: true },
  })
}

/** 取得目前使用者與角色判斷函式。 */
export function useAuth() {
  const { data: user, isLoading } = useMe()
  const qc = useQueryClient()
  const can = useCallback((min: Role) => hasRole(user, min), [user])

  const logout = useCallback(async () => {
    try {
      await authApi.logout()
    } catch {
      /* 即使失敗也清除本地狀態 */
    }
    qc.clear()
    window.location.assign('/login')
  }, [qc])

  return { user: user ?? null, loading: isLoading, can, logout }
}

export function useCan(min: Role): boolean {
  const { can } = useAuth()
  return can(min)
}
