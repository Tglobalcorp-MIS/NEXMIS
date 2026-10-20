// NEXMIS REST API v1 型別定義，對應 docs/api.md。
// 時間欄位皆為 RFC 3339（UTC）字串；日期欄位（purchase_date、warranty_end）為 YYYY-MM-DD。
// 文件未詳列欄位者（Hardware、Health、Battery、Software）沿用 internal/protocol 的 JSON 結構；
// 文件完全未定義者（LifecycleEvent、InventoryChange、Script、AuditEntry 列表外框等）以註解標示為「假設」。

export type ISODate = string

// ---------- 通用 ----------

export interface ListResponse<T> {
  items: T[]
  total: number
}

export interface ApiErrorBody {
  error: { code: ErrorCode | string; message: string }
}

export type ErrorCode =
  | 'bad_request'
  | 'unauthorized'
  | 'forbidden'
  | 'not_found'
  | 'conflict'
  | 'rate_limited'
  | 'internal'

export type Role = 'admin' | 'operator' | 'helpdesk' | 'viewer'

export interface Target {
  all: boolean
  device_ids: string[]
  group_ids: string[]
  tags: string[]
}

export interface PageParams {
  page?: number
  page_size?: number
}

// ---------- 1. 驗證 ----------

export interface User {
  id: string
  username: string
  display_name: string
  email: string
  source: 'local' | 'ad'
  role: Role
  disabled: boolean
  last_login_at: ISODate | null
  created_at: ISODate
}

export interface LoginRequest {
  username: string
  password: string
}

export interface ChangePasswordRequest {
  current_password: string
  new_password: string
}

// ---------- 2. 儀表板 ----------

export interface KeyCount {
  key: string
  count: number
}

export interface Dashboard {
  devices: { total: number; online: number; offline: number; with_agent: number; without_agent: number }
  by_type: KeyCount[]
  by_os_edition: KeyCount[]
  by_lifecycle: KeyCount[]
  health: { ok: number; warning: number; critical: number; unknown: number }
  battery_alerts: { device_id: string; device_name: string; health_percent: number }[]
  warranty_expiring: { device_id: string; device_name: string; warranty_end: string }[]
  top_issues: { code: string; severity: Severity; count: number }[]
  recent_commands: Command[]
  unread_chats: number
}

// ---------- 3. 裝置 ----------

export type DeviceType = 'windows' | 'server' | 'printer' | 'network' | 'storage' | 'other' | 'unknown'
export type DeviceSource = 'agent' | 'ad' | 'discovery' | 'manual'
export type LifecycleState = 'procurement' | 'in_stock' | 'in_use' | 'repair' | 'idle' | 'retired'
export type HealthStatus = 'ok' | 'warning' | 'critical' | 'unknown'
export type Severity = 'info' | 'warning' | 'critical'
export type ConsentPolicy = 'always_ask' | 'notify_only' | 'deny'

export interface Device {
  id: string
  name: string
  hostname: string
  domain: string
  device_type: DeviceType
  sources: DeviceSource[]
  has_agent: boolean
  online: boolean
  last_seen_at: ISODate | null
  os_name: string
  os_version: string
  os_edition: string
  manufacturer: string
  model: string
  serial_number: string
  chassis_type: string
  ip_addresses: string[] | null
  mac_addresses: string[] | null
  logged_in_user: string
  group_id: string | null
  group_name: string
  tags: string[] | null
  lifecycle_state: LifecycleState
  health_status: HealthStatus
  issue_count: number
  cpu_percent: number | null
  mem_percent: number | null
  battery_health: number | null
  agent_version: string
  asset_tag: string
  owner_name: string
  department: string
  location: string
  purchase_date: string | null
  warranty_end: string | null
  notes: string
  ad_dn: string
  ad_enabled: boolean | null
  ad_last_logon: ISODate | null
  created_at: ISODate
  updated_at: ISODate
}

export interface Issue {
  code: string
  severity: Severity
  message: string
}

export interface DeviceDetail extends Device {
  issues: Issue[] | null
  uptime_sec: number | null
  pending_reboot: boolean
  helper_online: boolean
  consent_policy: ConsentPolicy
  enrolled_at: ISODate | null
  first_seen_at: ISODate | null
}

export interface DeviceListParams extends PageParams {
  q?: string
  status?: 'online' | 'offline'
  type?: DeviceType
  lifecycle?: LifecycleState
  health?: HealthStatus
  group_id?: string
  tag?: string
  has_agent?: 'true' | 'false'
  sort?: string
}

/** POST /devices/bulk：add_tags 與 group_id 擇一或都給；group_id 為空字串表示移出群組。 */
export interface DeviceBulkRequest {
  device_ids: string[]
  add_tags?: string[]
  group_id?: string
}

export interface NewDevice {
  name: string
  device_type: DeviceType
  ip_addresses: string[]
  mac_addresses: string[]
  manufacturer?: string
  model?: string
  serial_number?: string
  group_id?: string | null
  tags?: string[]
  asset_tag?: string
  owner_name?: string
  department?: string
  location?: string
  notes?: string
}

export interface DevicePatch {
  name?: string
  group_id?: string | null
  tags?: string[]
  asset_tag?: string
  owner_name?: string
  department?: string
  location?: string
  purchase_date?: string | null
  warranty_end?: string | null
  notes?: string
  device_type?: DeviceType
}

/** 文件只寫 `LifecycleEvent[]`；欄位依 internal/server/store 現行實作。 */
export interface LifecycleEvent {
  id: number
  from_state: LifecycleState | ''
  to_state: LifecycleState
  reason: string
  actor_name: string
  at: ISODate
}

// ---- 盤點（沿用 internal/protocol） ----

export interface CPU {
  name: string
  cores: number
  logical_processors: number
  max_clock_mhz: number
}
export interface MemModule {
  slot: string
  capacity: number
  speed_mhz: number
  manufacturer: string
  part_number: string
}
export interface Disk {
  model: string
  serial_number: string
  size: number
  media_type: string
  bus_type: string
  health: string
}
export interface Volume {
  mount: string
  label: string
  file_system: string
  size: number
  free: number
}
export interface NIC {
  name: string
  mac: string
  ips: string[] | null
  gateway?: string[] | null
  dns?: string[] | null
  dhcp: boolean
  speed_mbps: number
  type: string
}
export interface GPU {
  name: string
  driver_version: string
  vram: number
}
export interface Monitor {
  manufacturer: string
  model: string
  serial_number: string
}
export interface BitLocker {
  mount: string
  protection_status: string
}

export interface Hardware {
  manufacturer: string
  model: string
  serial_number: string
  chassis_type: string
  bios_vendor: string
  bios_version: string
  bios_date: string
  os_name: string
  os_version: string
  os_build: string
  os_edition: string
  os_install_date: string
  os_arch: string
  domain: string
  domain_joined: boolean
  cpus: CPU[] | null
  memory_total: number
  memory_modules: MemModule[] | null
  disks: Disk[] | null
  volumes: Volume[] | null
  nics: NIC[] | null
  gpus: GPU[] | null
  monitors: Monitor[] | null
  tpm_present: boolean
  tpm_version: string
  secure_boot: boolean
  bitlocker?: BitLocker[] | null
}

export interface InventoryResponse {
  hardware: Hardware | null
  hardware_collected_at: ISODate | null
  software_collected_at: ISODate | null
  software_count: number
}

export interface Software {
  name: string
  version: string
  publisher: string
  install_date?: string
  arch?: string
  scope?: string
  uninstall_id?: string
}

/** 文件只寫 `InventoryChange[]`；欄位依 internal/server/store 現行實作。 */
export interface InventoryChange {
  id: number
  kind: 'hardware' | 'software' | string
  change: 'added' | 'removed' | 'changed' | string
  item: string
  old_value: string
  new_value: string
  at: ISODate
}

// ---- 健康 ----

export interface EventSample {
  log: string
  source: string
  event_id: number
  count: number
  last_time: ISODate
  message: string
}

export interface ProcessInfo {
  name: string
  pid: number
  cpu: number
  mem_bytes: number
}

export interface Health {
  collected: ISODate
  cpu_percent: number
  mem_percent: number
  uptime_sec: number
  pending_reboot: boolean
  pending_reboot_reasons?: string[] | null
  last_boot: ISODate
  volumes: Volume[] | null
  disk_health: Disk[] | null
  defender_enabled?: boolean | null
  defender_realtime?: boolean | null
  defender_sig_age_days?: number | null
  firewall_enabled?: boolean | null
  last_update_install?: ISODate | null
  event_errors_24h: number
  top_events?: EventSample[] | null
  stopped_auto_services?: string[] | null
  top_processes?: ProcessInfo[] | null
  issues?: Issue[] | null
}

export interface HealthPoint {
  collected_at: ISODate
  cpu_percent: number | null
  mem_percent: number | null
  status: HealthStatus
}

export interface HealthResponse {
  latest: Health | null
  history: HealthPoint[] | null
}

// ---- 電池 ----

export interface Battery {
  collected: ISODate
  present: boolean
  name?: string
  manufacturer?: string
  serial_number?: string
  chemistry?: string
  design_capacity_mwh?: number
  full_charge_capacity_mwh?: number
  cycle_count?: number
  health_percent?: number
  charge_percent?: number
  on_ac: boolean
  estimated_runtime_min?: number
}

export interface BatteryPoint {
  collected_at: ISODate
  health_percent: number | null
  full_charge_capacity_mwh: number | null
  design_capacity_mwh: number | null
  cycle_count: number | null
}

export interface BatteryResponse {
  latest: Battery | null
  history: BatteryPoint[] | null
}

// ---- 指令 ----

export type CommandKind =
  | 'powershell'
  | 'cmd'
  | 'reboot'
  | 'shutdown'
  | 'refresh'
  | 'notify'
  | 'lock'
  | 'logoff'
  | 'service.restart'
  | 'process.kill'
  | 'uninstall'
  | 'install'
  | 'agent.update'
  | 'discovery.scan'

export type CommandStatus =
  | 'queued'
  | 'sent'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'timeout'
  | 'canceled'
  | 'expired'
  | 'rejected'

export interface RebootArgs {
  force: boolean
  countdown_sec: number
  message?: string
  allow_defer: boolean
}

export interface NotifyArgs {
  title: string
  message: string
  level?: 'info' | 'warning' | 'critical'
}

export type CommandArgs = Record<string, unknown>

export interface NewCommand {
  kind: CommandKind
  script?: string
  timeout_sec?: number
  args?: CommandArgs
}

export interface Command {
  id: string
  device_id: string
  device_name: string
  kind: CommandKind
  args: CommandArgs | null
  status: CommandStatus
  exit_code: number | null
  stdout: string
  stderr: string
  truncated: boolean
  issued_by_name: string
  timeout_sec: number
  created_at: ISODate
  sent_at: ISODate | null
  started_at: ISODate | null
  finished_at: ISODate | null
  expires_at: ISODate | null
  /** 依 kind 而定的結構化結果（選填） */
  data?: unknown
  deployment_id: string | null
  schedule_id: string | null
}

export interface CommandListParams extends PageParams {
  status?: CommandStatus
  kind?: CommandKind
  device_id?: string
}

export interface BatchCommandRequest {
  target: Target
  command: NewCommand
}

export interface BatchCommandResponse {
  count: number
  command_ids: string[]
}

// ---- 訊息 ----

export interface ChatMessage {
  id: string
  device_id: string
  direction: 'mis' | 'user'
  sender: string
  body: string
  created_at: ISODate
  delivered_at: ISODate | null
  read_at: ISODate | null
}

// ---------- 4. 遠端連線 ----------

export type RemoteMode = 'view' | 'control'
export type RemoteStatus = 'pending' | 'active' | 'denied' | 'ended' | 'failed'

export interface RemoteSession {
  id: string
  device_id: string
  device_name: string
  mode: RemoteMode
  status: RemoteStatus
  requester_name: string
  reason: string
  consent_user: string
  created_at: ISODate
  started_at: ISODate | null
  ended_at: ISODate | null
  end_reason: string
}

export interface RemoteRequest {
  mode: RemoteMode
  reason: string
}

/** 遠端檢視 WS：伺服器 → 瀏覽器 文字訊框 */
export interface RemoteStatusFrame {
  t: 'status'
  status: 'waiting_consent' | 'active' | 'ended' | 'denied'
  reason?: string
}

/** 遠端檢視 WS：0x01 螢幕資訊 */
export interface RemoteScreenInfo {
  width: number
  height: number
  monitor: number
  monitors: number
}

/** 遠端檢視 WS：瀏覽器 → 伺服器 */
export type RemoteClientMessage =
  | { t: 'ack'; seq: number }
  | { t: 'mouse'; a: 'move' | 'down' | 'up' | 'wheel'; x: number; y: number; b?: number; d?: number }
  | { t: 'key'; a: 'down' | 'up'; code: string; key: string }
  | { t: 'text'; s: string }
  | { t: 'quality'; q: number }
  | { t: 'monitor'; i: number }
  | { t: 'cad' }

// ---------- 5. 即時事件 ----------

export type ServerEvent =
  | { type: 'device.online'; data: { device_id: string; name: string } }
  | { type: 'device.offline'; data: { device_id: string; name: string } }
  | { type: 'device.updated'; data: Device }
  | { type: 'command.updated'; data: Command }
  | { type: 'chat.message'; data: ChatMessage }
  | { type: 'remote.updated'; data: RemoteSession }
  | { type: 'deployment.updated'; data: Deployment }
  | { type: 'scan.updated'; data: DiscoveryScan }

// ---------- 6. 裝置群組 ----------

export interface Group {
  id: string
  name: string
  description: string
  consent_policy: ConsentPolicy
  device_count: number
}

export interface GroupInput {
  name: string
  description: string
  consent_policy: ConsentPolicy
}

// ---------- 7. 設備探索 ----------

export interface DiscoveryScan {
  id: string
  cidrs: string[]
  status: 'running' | 'completed' | 'failed'
  found: number
  requested_by_name: string
  probe_device_name: string | null
  started_at: ISODate
  finished_at: ISODate | null
  error: string
}

export interface DiscoveredHost {
  ip: string
  mac: string
  hostname: string
  open_ports: number[] | null
  vendor: string
  device_type: DeviceType | string
  device_id: string | null
  rtt_ms: number
}

export interface DiscoveryScanDetail extends DiscoveryScan {
  hosts: DiscoveredHost[] | null
}

export interface NewScan {
  cidrs: string[]
  ports?: number[]
  probe_device_id?: string
}

export interface ImportHostsRequest {
  scan_id: string
  ips: string[]
}

// ---------- 8. 軟體套件與部署 ----------

export type PackageType = 'msi' | 'exe' | 'msix' | 'ps1'

export interface Package {
  id: string
  name: string
  version: string
  publisher: string
  file_name: string
  file_size: number
  sha256: string
  type: PackageType
  install_args: string
  uninstall_args: string
  success_codes: number[] | null
  created_by_name: string
  created_at: ISODate
}

export interface PackagePatch {
  name?: string
  version?: string
  publisher?: string
  install_args?: string
  uninstall_args?: string
  success_codes?: number[]
}

export type DeploymentStatus = 'scheduled' | 'running' | 'completed' | 'canceled'

export interface Deployment {
  id: string
  name: string
  package_id: string
  package_name: string
  target: Target
  status: DeploymentStatus
  scheduled_at: ISODate | null
  total: number
  succeeded: number
  failed: number
  pending: number
  created_by_name: string
  created_at: ISODate
}

export interface DeploymentResult {
  device_id: string
  device_name: string
  status: CommandStatus | string
  exit_code: number | null
  finished_at: ISODate | null
  command_id: string | null
}

export interface DeploymentDetail extends Deployment {
  results: DeploymentResult[] | null
}

export interface NewDeployment {
  name: string
  package_id: string
  target: Target
  scheduled_at?: ISODate
}

// ---------- 9. 指令庫與排程 ----------

export type ScriptShell = 'powershell' | 'cmd'

/** 文件只定義寫入欄位；id、時間等為假設。 */
export interface Script {
  id: string
  name: string
  description: string
  shell: ScriptShell
  body: string
  timeout_sec: number
  created_by_name?: string
  created_at?: ISODate
  updated_at?: ISODate
}

export interface ScriptInput {
  name: string
  description: string
  shell: ScriptShell
  body: string
  timeout_sec: number
}

export type ScheduleAction =
  | { type: 'command'; command: NewCommand }
  | { type: 'deployment'; package_id: string }
  | { type: 'refresh' }

export interface Schedule {
  id: string
  name: string
  cron: string
  enabled: boolean
  action: ScheduleAction
  target: Target
  last_run_at: ISODate | null
  next_run_at: ISODate | null
  created_by_name: string
}

export interface ScheduleInput {
  name: string
  cron: string
  enabled: boolean
  action: ScheduleAction
  target: Target
}

// ---------- 10. 稽核 ----------

export interface AuditEntry {
  id: number
  at: ISODate
  actor_name: string
  actor_ip: string
  action: string
  target_type: string
  target_id: string
  target_name: string
  details: unknown
  result: 'success' | 'failure'
}

export interface AuditParams extends PageParams {
  actor?: string
  action?: string
  target_id?: string
  from?: string
  to?: string
  q?: string
}

// ---------- 11. 系統設定 ----------

export interface ADRoleMapping {
  group_dn: string
  role: Role
}

export interface ADSettings {
  enabled: boolean
  url: string
  base_dn: string
  bind_dn: string
  bind_password_set: boolean
  computer_base_dn: string
  user_base_dn: string
  domain: string
  netbios: string
  insecure_skip_verify: boolean
  ca_cert_pem: string
  sync_interval_min: number
  role_mappings: ADRoleMapping[] | null
}

/** PUT /settings/ad：bind_password 只寫不讀，留空表示不變更。 */
export interface ADSettingsInput extends Omit<ADSettings, 'bind_password_set'> {
  bind_password?: string
}

export interface ADTestResult {
  ok: boolean
  message: string
  sample_computers: string[] | null
}

export interface ADSyncResult {
  added: number
  updated: number
  total: number
}

export interface AgentConfig {
  heartbeat_sec: number
  health_sec: number
  battery_sec: number
  hardware_sec: number
  software_sec: number
  consent_policy: ConsentPolicy
  consent_timeout_sec: number
  reboot_countdown_sec: number
  reboot_max_defer: number
  update_check_sec: number
}

export type AgentVersionState = 'current' | 'outdated' | 'newer' | 'unknown'

export interface AgentUpdateStatus {
  version: string
  platforms: string[]
  auto_update: boolean
  auto_update_source: 'manifest' | 'settings'
  manifest_auto_update: boolean
  update_check_sec: number
  connected_agents: number
  summary: { total: number; current: number; outdated: number; offline: number; unknown: number }
  versions: { version: string; online: number; offline: number; state: AgentVersionState }[]
  last_push_at: string | null
  last_push_agents: number
  last_push_reason?: string
}

export interface AgentUpdateSettings {
  /** null 表示沿用 manifest.json 的 auto_update */
  auto_update: boolean | null
}

export interface GeneralSettings {
  server_url: string
  timezone: string
  data_retention_days: number
  battery_alert_percent: number
  warranty_alert_days: number
}

export interface EnrollmentToken {
  id: string
  name: string
  token_prefix: string
  expires_at: ISODate | null
  max_uses: number | null
  uses: number
  group_id: string | null
  tags: string[] | null
  revoked: boolean
  created_by_name: string | null
  created_at: ISODate
}

export interface NewEnrollmentToken {
  name: string
  expires_at?: ISODate
  max_uses?: number
  group_id?: string
  tags?: string[]
}

/**
 * 建立回應：EnrollmentToken + 明文 token（只顯示一次）+ 伺服器組好的單行安裝指令
 * （-EncodedCommand，PowerShell 與命令提示字元皆可貼上）。內建 TLS 憑證未受系統信任
 * （例如自簽）時，install_pin 為其 SHA-256 指紋，安裝指令已帶入。
 */
export interface CreatedEnrollmentToken extends EnrollmentToken {
  token: string
  install_command: string
  install_pin: string
}

export interface NewUser {
  username: string
  display_name: string
  email: string
  role: Role
  password: string
}

export interface UserPatch {
  display_name?: string
  email?: string
  role?: Role
  disabled?: boolean
  password?: string
}

export interface SystemInfo {
  version: string
  build_time: string
  go_version: string
  db_version: string
  /** Agent 通訊協定版本 */
  protocol?: number
  agents_connected: number
  uptime_sec: number
  agent_latest_version: string
  agent_installer_available: boolean
}
