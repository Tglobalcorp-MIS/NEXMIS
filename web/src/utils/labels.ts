import type {
  CommandKind,
  CommandStatus,
  ConsentPolicy,
  DeploymentStatus,
  DeviceSource,
  DeviceType,
  HealthStatus,
  LifecycleState,
  PackageType,
  RemoteMode,
  RemoteStatus,
  Role,
  Severity,
} from '../api/types'

export const lifecycleLabels: Record<LifecycleState, string> = {
  procurement: '採購中',
  in_stock: '庫存',
  in_use: '使用中',
  repair: '維修中',
  idle: '閒置',
  retired: '報廢',
}
export const lifecycleColors: Record<LifecycleState, string> = {
  procurement: 'purple',
  in_stock: 'cyan',
  in_use: 'green',
  repair: 'orange',
  idle: 'default',
  retired: 'red',
}
export const lifecycleOrder: LifecycleState[] = ['procurement', 'in_stock', 'in_use', 'repair', 'idle', 'retired']

export const healthLabels: Record<HealthStatus, string> = {
  ok: '正常',
  warning: '警告',
  critical: '嚴重',
  unknown: '未知',
}
export const healthColors: Record<HealthStatus, string> = {
  ok: 'success',
  warning: 'warning',
  critical: 'error',
  unknown: 'default',
}
export const healthChartColors: Record<HealthStatus, string> = {
  ok: '#52c41a',
  warning: '#faad14',
  critical: '#ff4d4f',
  unknown: '#bfbfbf',
}

export const severityLabels: Record<Severity, string> = { info: '資訊', warning: '警告', critical: '嚴重' }
export const severityColors: Record<Severity, string> = { info: 'blue', warning: 'orange', critical: 'red' }

export const deviceTypeLabels: Record<DeviceType, string> = {
  windows: 'Windows 電腦',
  server: '伺服器',
  printer: '印表機',
  network: '網路設備',
  storage: '儲存設備',
  other: '其他',
  unknown: '未知',
}

export const sourceLabels: Record<DeviceSource, string> = {
  agent: 'Agent',
  ad: 'AD',
  discovery: '探索',
  manual: '手動',
}

export const commandStatusLabels: Record<CommandStatus, string> = {
  queued: '排隊中',
  sent: '已送出',
  running: '執行中',
  succeeded: '成功',
  failed: '失敗',
  timeout: '逾時',
  canceled: '已取消',
  expired: '已過期',
  rejected: '已拒絕',
}
export const commandStatusColors: Record<CommandStatus, string> = {
  queued: 'default',
  sent: 'processing',
  running: 'processing',
  succeeded: 'success',
  failed: 'error',
  timeout: 'warning',
  canceled: 'default',
  expired: 'default',
  rejected: 'error',
}
export const commandPendingStatuses: CommandStatus[] = ['queued', 'sent']

export const commandKindLabels: Record<CommandKind, string> = {
  powershell: 'PowerShell',
  cmd: 'CMD',
  reboot: '重新開機',
  shutdown: '關機',
  refresh: '重新整理盤點',
  notify: '傳送通知',
  lock: '鎖定',
  logoff: '登出',
  'service.restart': '重新啟動服務',
  'process.kill': '結束處理程序',
  uninstall: '解除安裝',
  install: '安裝軟體',
  'agent.update': '更新 Agent',
  'discovery.scan': '探索掃描',
}

export const remoteStatusLabels: Record<RemoteStatus, string> = {
  pending: '等待同意',
  active: '連線中',
  denied: '已拒絕',
  ended: '已結束',
  failed: '失敗',
}
export const remoteStatusColors: Record<RemoteStatus, string> = {
  pending: 'processing',
  active: 'green',
  denied: 'red',
  ended: 'default',
  failed: 'error',
}
export const remoteModeLabels: Record<RemoteMode, string> = { view: '僅檢視', control: '可操作' }

export const deploymentStatusLabels: Record<DeploymentStatus, string> = {
  scheduled: '已排程',
  running: '執行中',
  completed: '已完成',
  canceled: '已取消',
}
export const deploymentStatusColors: Record<DeploymentStatus, string> = {
  scheduled: 'blue',
  running: 'processing',
  completed: 'success',
  canceled: 'default',
}

export const roleLabels: Record<Role, string> = {
  admin: '系統管理員',
  operator: '維運人員',
  helpdesk: '服務台',
  viewer: '唯讀',
}
export const roleColors: Record<Role, string> = {
  admin: 'red',
  operator: 'blue',
  helpdesk: 'green',
  viewer: 'default',
}

export const consentLabels: Record<ConsentPolicy, string> = {
  always_ask: '每次詢問使用者',
  notify_only: '僅通知（不需同意）',
  deny: '禁止遠端連線',
}

export const packageTypeLabels: Record<PackageType, string> = {
  msi: 'MSI',
  exe: 'EXE',
  msix: 'MSIX',
  ps1: 'PowerShell 指令碼',
}

export const scanStatusLabels: Record<string, string> = { running: '掃描中', completed: '已完成', failed: '失敗' }
export const scanStatusColors: Record<string, string> = { running: 'processing', completed: 'success', failed: 'error' }

export const issueLabels: Record<string, string> = {
  disk_low: '磁碟空間不足',
  disk_unhealthy: '磁碟健康異常',
  mem_high: '記憶體使用率過高',
  cpu_high: 'CPU 使用率過高',
  pending_reboot: '待重新開機',
  uptime_long: '長時間未重新開機',
  defender_off: 'Defender 未啟用',
  defender_realtime_off: 'Defender 即時保護關閉',
  defender_sig_old: 'Defender 病毒碼過舊',
  firewall_off: '防火牆未啟用',
  updates_stale: 'Windows Update 過久未安裝',
  event_errors: '事件記錄錯誤過多',
  battery_degraded: '電池老化',
  battery_cycles: '電池循環次數偏高',
}
export const issueLabel = (code: string) => issueLabels[code] ?? code

export function labelOf<K extends string>(map: Record<K, string>, key: string | null | undefined): string {
  if (!key) return '-'
  return (map as Record<string, string>)[key] ?? key
}

export function toOptions<K extends string>(map: Record<K, string>) {
  return (Object.keys(map) as K[]).map((value) => ({ value, label: map[value] }))
}

// 遠端連線結束原因（伺服器與 Agent 回報的代碼）
export const remoteEndReasonLabels: Record<string, string> = {
  denied: '使用者拒絕了請求',
  timeout: '使用者未在時限內回應',
  consent_timeout: '等待同意逾時',
  no_user: '目前沒有使用者登入',
  policy: '同意政策禁止遠端連線',
  helper_unavailable: '使用者端助手未執行',
  device_offline: '裝置離線',
  mis_stopped: '管理者結束了連線',
  viewer_closed: '檢視端已關閉',
  user_stopped: '使用者中斷了連線',
  helper_error: '使用者端無法擷取畫面',
  user: '使用者中斷了連線',
  mis: '管理者結束了連線',
  system: '系統結束了連線',
}
export const remoteEndReasonLabel = (r?: string | null) => {
  if (!r) return ''
  if (remoteEndReasonLabels[r]) return remoteEndReasonLabels[r]
  if (r.startsWith('user_')) return `使用者端結束（${r.slice(5)}）`
  return r
}
