import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'
import { useMe } from '../hooks/useAuth'
import { FullPageSpin } from './FullPageSpin'

export function RequireAuth({ children }: { children: ReactNode }) {
  const { data, isLoading } = useMe()
  const loc = useLocation()
  if (isLoading) return <FullPageSpin />
  if (!data) return <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />
  return <>{children}</>
}
