import type { ReactNode } from 'react'
import { Result } from 'antd'
import type { Role } from '../api/types'
import { useAuth } from '../hooks/useAuth'

/** 角色不足時不渲染子元素（或渲染 fallback）。 */
export function RoleGate({ min, children, fallback = null }: { min: Role; children: ReactNode; fallback?: ReactNode }) {
  const { can } = useAuth()
  return <>{can(min) ? children : fallback}</>
}

export function RequireRole({ min, children }: { min: Role; children: ReactNode }) {
  const { can } = useAuth()
  if (!can(min)) return <Result status="403" title="權限不足" subTitle="您的角色無法存取此頁面，請聯絡系統管理員。" />
  return <>{children}</>
}
