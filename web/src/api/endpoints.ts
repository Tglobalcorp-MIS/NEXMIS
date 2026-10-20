import { http, normalizeList, upload } from './client'
import type {
  ADSettings,
  ADSettingsInput,
  ADSyncResult,
  ADTestResult,
  AgentConfig,
  AgentUpdateSettings,
  AgentUpdateStatus,
  AuditEntry,
  AuditParams,
  BatchCommandRequest,
  BatchCommandResponse,
  BatteryResponse,
  ChangePasswordRequest,
  ChatMessage,
  Command,
  CommandListParams,
  CreatedEnrollmentToken,
  Dashboard,
  Deployment,
  DeploymentDetail,
  Device,
  DeviceBulkRequest,
  DeviceDetail,
  DeviceListParams,
  DevicePatch,
  DiscoveryScan,
  DiscoveryScanDetail,
  EnrollmentToken,
  GeneralSettings,
  Group,
  GroupInput,
  HealthResponse,
  ImportHostsRequest,
  InventoryChange,
  InventoryResponse,
  LifecycleEvent,
  LifecycleState,
  ListResponse,
  LoginRequest,
  NewCommand,
  NewDeployment,
  NewDevice,
  NewEnrollmentToken,
  NewScan,
  NewUser,
  Package,
  PackagePatch,
  PageParams,
  RemoteRequest,
  RemoteSession,
  Schedule,
  ScheduleInput,
  Script,
  ScriptInput,
  Software,
  SystemInfo,
  User,
  UserPatch,
} from './types'

type MaybeList<T> = T[] | ListResponse<T>

const enc = encodeURIComponent

// ---------- 驗證 ----------
export const authApi = {
  login: (body: LoginRequest) => http.post<User>('/auth/login', body),
  logout: () => http.post<void>('/auth/logout'),
  me: () => http.get<User>('/auth/me'),
  changePassword: (body: ChangePasswordRequest) => http.post<void>('/auth/password', body),
}

// ---------- 儀表板 ----------
export const dashboardApi = {
  // Go 的 nil slice 會序列化為 null，這裡統一補成空陣列
  get: () =>
    http.get<Dashboard>('/dashboard').then((d) => ({
      ...d,
      by_type: d.by_type ?? [],
      by_os_edition: d.by_os_edition ?? [],
      by_lifecycle: d.by_lifecycle ?? [],
      health: Object.assign({ ok: 0, warning: 0, critical: 0, unknown: 0 }, d.health),
      battery_alerts: d.battery_alerts ?? [],
      warranty_expiring: d.warranty_expiring ?? [],
      top_issues: d.top_issues ?? [],
      recent_commands: d.recent_commands ?? [],
    })),
}

// ---------- 裝置 ----------
export const deviceApi = {
  list: (params: DeviceListParams) =>
    http.get<MaybeList<Device>>('/devices', { ...params }).then(normalizeList),
  get: (id: string) => http.get<DeviceDetail>(`/devices/${enc(id)}`),
  create: (body: NewDevice) => http.post<Device>('/devices', body),
  update: (id: string, body: DevicePatch) => http.patch<Device>(`/devices/${enc(id)}`, body),
  remove: (id: string) => http.del(`/devices/${enc(id)}`),
  bulk: (body: DeviceBulkRequest) => http.post<{ updated: number }>('/devices/bulk', body),
  remoteSessions: (id: string, params: PageParams) =>
    http.get<MaybeList<RemoteSession>>(`/devices/${enc(id)}/remote-sessions`, { ...params }).then(normalizeList),
  setLifecycle: (id: string, body: { state: LifecycleState; reason: string }) =>
    http.post<unknown>(`/devices/${enc(id)}/lifecycle`, body),
  lifecycle: (id: string) =>
    http.get<MaybeList<LifecycleEvent>>(`/devices/${enc(id)}/lifecycle`).then((r) => normalizeList(r).items),
  inventory: (id: string) => http.get<InventoryResponse>(`/devices/${enc(id)}/inventory`),
  software: (id: string, params: { q?: string } & PageParams) =>
    http.get<MaybeList<Software>>(`/devices/${enc(id)}/software`, { ...params }).then(normalizeList),
  inventoryChanges: (id: string) =>
    http
      .get<MaybeList<InventoryChange>>(`/devices/${enc(id)}/inventory/changes`)
      .then((r) => normalizeList(r).items),
  health: (id: string, days: number) => http.get<HealthResponse>(`/devices/${enc(id)}/health`, { days }),
  battery: (id: string, days: number) => http.get<BatteryResponse>(`/devices/${enc(id)}/battery`, { days }),
  commands: (id: string, params: PageParams) =>
    http.get<MaybeList<Command>>(`/devices/${enc(id)}/commands`, { ...params }).then(normalizeList),
  runCommand: (id: string, body: NewCommand) => http.post<Command>(`/devices/${enc(id)}/commands`, body),
  messages: (id: string, before?: string) =>
    http
      .get<MaybeList<ChatMessage>>(`/devices/${enc(id)}/messages`, { before })
      .then((r) => normalizeList(r).items),
  sendMessage: (id: string, body: string) => http.post<ChatMessage>(`/devices/${enc(id)}/messages`, { body }),
  markRead: (id: string) => http.post<void>(`/devices/${enc(id)}/messages/read`),
  requestRemote: (id: string, body: RemoteRequest) => http.post<RemoteSession>(`/devices/${enc(id)}/remote`, body),
}

// ---------- 指令 ----------
export const commandApi = {
  batch: (body: BatchCommandRequest) => http.post<BatchCommandResponse>('/commands/batch', body),
  list: (params: CommandListParams) =>
    http.get<MaybeList<Command>>('/commands', { ...params }).then(normalizeList),
  get: (id: string) => http.get<Command>(`/commands/${enc(id)}`),
  cancel: (id: string) => http.post<void>(`/commands/${enc(id)}/cancel`),
}

// ---------- 遠端 ----------
export const remoteApi = {
  list: (params: PageParams & { status?: string }) =>
    http.get<MaybeList<RemoteSession>>('/remote-sessions', { ...params }).then(normalizeList),
  get: (id: string) => http.get<RemoteSession>(`/remote-sessions/${enc(id)}`),
  stop: (id: string) => http.post<void>(`/remote-sessions/${enc(id)}/stop`),
}

// ---------- 群組 ----------
export const groupApi = {
  list: () => http.get<MaybeList<Group>>('/groups').then((r) => normalizeList(r).items),
  create: (body: GroupInput) => http.post<Group>('/groups', body),
  update: (id: string, body: Partial<GroupInput>) => http.patch<Group>(`/groups/${enc(id)}`, body),
  remove: (id: string) => http.del(`/groups/${enc(id)}`),
}

// ---------- 探索 ----------
export const discoveryApi = {
  scans: (params: PageParams) =>
    http.get<MaybeList<DiscoveryScan>>('/discovery/scans', { ...params }).then(normalizeList),
  scan: (id: string) => http.get<DiscoveryScanDetail>(`/discovery/scans/${enc(id)}`),
  create: (body: NewScan) => http.post<DiscoveryScan>('/discovery/scans', body),
  importHosts: (body: ImportHostsRequest) => http.post<unknown>('/discovery/hosts/import', body),
}

// ---------- 套件與部署 ----------
export const packageApi = {
  list: () => http.get<MaybeList<Package>>('/packages', { page_size: 500 }).then((r) => normalizeList(r).items),
  upload: (form: FormData, onProgress?: (p: number) => void) => upload<Package>('/packages', form, onProgress),
  update: (id: string, body: PackagePatch) => http.patch<Package>(`/packages/${enc(id)}`, body),
  remove: (id: string) => http.del(`/packages/${enc(id)}`),
}

export const deploymentApi = {
  list: (params: PageParams) =>
    http.get<MaybeList<Deployment>>('/deployments', { ...params }).then(normalizeList),
  get: (id: string) => http.get<DeploymentDetail>(`/deployments/${enc(id)}`),
  create: (body: NewDeployment) => http.post<Deployment>('/deployments', body),
  cancel: (id: string) => http.post<void>(`/deployments/${enc(id)}/cancel`),
  retry: (id: string) => http.post<void>(`/deployments/${enc(id)}/retry`),
}

// ---------- 指令庫與排程 ----------
export const scriptApi = {
  list: () => http.get<MaybeList<Script>>('/scripts', { page_size: 500 }).then((r) => normalizeList(r).items),
  create: (body: ScriptInput) => http.post<Script>('/scripts', body),
  update: (id: string, body: Partial<ScriptInput>) => http.patch<Script>(`/scripts/${enc(id)}`, body),
  remove: (id: string) => http.del(`/scripts/${enc(id)}`),
}

export const scheduleApi = {
  list: () => http.get<MaybeList<Schedule>>('/schedules', { page_size: 500 }).then((r) => normalizeList(r).items),
  create: (body: ScheduleInput) => http.post<Schedule>('/schedules', body),
  update: (id: string, body: Partial<ScheduleInput>) => http.patch<Schedule>(`/schedules/${enc(id)}`, body),
  remove: (id: string) => http.del(`/schedules/${enc(id)}`),
  run: (id: string) => http.post<unknown>(`/schedules/${enc(id)}/run`),
}

// ---------- 稽核 ----------
export const auditApi = {
  list: (params: AuditParams) => http.get<MaybeList<AuditEntry>>('/audit', { ...params }).then(normalizeList),
}

// ---------- 系統設定 ----------
export const settingsApi = {
  getAD: () => http.get<ADSettings>('/settings/ad'),
  putAD: (body: ADSettingsInput) => http.put<ADSettings>('/settings/ad', body),
  testAD: (body?: ADSettingsInput) => http.post<ADTestResult>('/settings/ad/test', body ?? {}),
  syncAD: () => http.post<ADSyncResult>('/settings/ad/sync'),
  getAgent: () => http.get<AgentConfig>('/settings/agent'),
  putAgent: (body: AgentConfig) => http.put<AgentConfig>('/settings/agent', body),
  getAgentUpdate: () => http.get<AgentUpdateStatus>('/settings/agent-update'),
  putAgentUpdate: (body: AgentUpdateSettings) => http.put<AgentUpdateStatus>('/settings/agent-update', body),
  pushAgentUpdate: () => http.post<{ version: string; agents: number }>('/settings/agent-update/push'),
  getGeneral: () => http.get<GeneralSettings>('/settings/general'),
  putGeneral: (body: GeneralSettings) => http.put<GeneralSettings>('/settings/general', body),
  systemInfo: () => http.get<SystemInfo>('/system/info'),
}

export const tokenApi = {
  list: () => http.get<MaybeList<EnrollmentToken>>('/enrollment-tokens').then((r) => normalizeList(r).items),
  create: (body: NewEnrollmentToken) => http.post<CreatedEnrollmentToken>('/enrollment-tokens', body),
  revoke: (id: string) => http.del(`/enrollment-tokens/${enc(id)}`),
}

export const userApi = {
  list: () => http.get<MaybeList<User>>('/users', { page_size: 500 }).then((r) => normalizeList(r).items),
  create: (body: NewUser) => http.post<User>('/users', body),
  update: (id: string, body: UserPatch) => http.patch<User>(`/users/${enc(id)}`, body),
  remove: (id: string) => http.del(`/users/${enc(id)}`),
}
