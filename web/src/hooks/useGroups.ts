import { useQuery } from '@tanstack/react-query'
import { groupApi } from '../api/endpoints'
import { qk } from '../api/keys'

export function useGroups() {
  return useQuery({ queryKey: qk.groups, queryFn: groupApi.list, staleTime: 60_000 })
}

/** 群組 id → 名稱 */
export function useGroupName() {
  const { data } = useGroups()
  return (id: string) => data?.find((g) => g.id === id)?.name
}
