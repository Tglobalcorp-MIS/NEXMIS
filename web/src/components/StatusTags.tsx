import { Badge, Tag, Tooltip } from 'antd'
import type {
  CommandStatus,
  DeploymentStatus,
  HealthStatus,
  LifecycleState,
  RemoteStatus,
  Role,
  Severity,
} from '../api/types'
import {
  commandStatusColors,
  commandStatusLabels,
  deploymentStatusColors,
  deploymentStatusLabels,
  healthColors,
  healthLabels,
  labelOf,
  lifecycleColors,
  lifecycleLabels,
  remoteStatusColors,
  remoteStatusLabels,
  roleColors,
  roleLabels,
  scanStatusColors,
  scanStatusLabels,
  severityColors,
  severityLabels,
} from '../utils/labels'
import { fmtRelative, fmtTime } from '../utils/format'

export function OnlineStatus({
  online,
  hasAgent = true,
  lastSeen,
  showText = true,
}: {
  online: boolean
  hasAgent?: boolean
  lastSeen?: string | null
  showText?: boolean
}) {
  if (!hasAgent) {
    return (
      <Tooltip title="未安裝 Agent">
        <Badge status="default" text={showText ? '無 Agent' : undefined} />
      </Tooltip>
    )
  }
  const badge = <Badge status={online ? 'success' : 'error'} text={showText ? (online ? '線上' : '離線') : undefined} />
  if (!lastSeen || online) return badge
  return <Tooltip title={`最後上線：${fmtTime(lastSeen)}（${fmtRelative(lastSeen)}）`}>{badge}</Tooltip>
}

export function HealthTag({ status }: { status: HealthStatus | null | undefined }) {
  const s = status ?? 'unknown'
  return <Tag color={healthColors[s] ?? 'default'}>{labelOf(healthLabels, s)}</Tag>
}

export function SeverityTag({ severity }: { severity: Severity }) {
  return <Tag color={severityColors[severity] ?? 'default'}>{labelOf(severityLabels, severity)}</Tag>
}

export function CommandStatusTag({ status }: { status: CommandStatus | string }) {
  return (
    <Tag color={(commandStatusColors as Record<string, string>)[status] ?? 'default'}>
      {labelOf(commandStatusLabels, status)}
    </Tag>
  )
}

export function LifecycleTag({ state }: { state: LifecycleState | null | undefined }) {
  if (!state) return <Tag>-</Tag>
  return <Tag color={lifecycleColors[state] ?? 'default'}>{labelOf(lifecycleLabels, state)}</Tag>
}

export function RemoteStatusTag({ status }: { status: RemoteStatus }) {
  return <Tag color={remoteStatusColors[status] ?? 'default'}>{labelOf(remoteStatusLabels, status)}</Tag>
}

export function DeploymentStatusTag({ status }: { status: DeploymentStatus }) {
  return <Tag color={deploymentStatusColors[status] ?? 'default'}>{labelOf(deploymentStatusLabels, status)}</Tag>
}

export function ScanStatusTag({ status }: { status: string }) {
  return <Tag color={scanStatusColors[status] ?? 'default'}>{labelOf(scanStatusLabels, status)}</Tag>
}

export function RoleTag({ role }: { role: Role }) {
  return <Tag color={roleColors[role] ?? 'default'}>{labelOf(roleLabels, role)}</Tag>
}

export function BoolTag({ value, yes = '是', no = '否', invert = false }: { value: boolean | null | undefined; yes?: string; no?: string; invert?: boolean }) {
  if (value === null || value === undefined) return <Tag>未知</Tag>
  const good = invert ? !value : value
  return <Tag color={good ? 'success' : 'error'}>{value ? yes : no}</Tag>
}
