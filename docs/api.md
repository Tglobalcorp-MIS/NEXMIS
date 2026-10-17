# NEXMIS REST API（v1）

基底路徑：`/api/v1`　格式：JSON（snake_case）、時間一律 RFC 3339（UTC）

## 通用慣例

- **驗證**：登入後伺服器設定 `nexmis_session` Cookie（HttpOnly、Secure、SameSite=Strict）。所有寫入請求需帶 `X-NEXMIS-CSRF: 1` 標頭（配合 Origin 檢查防 CSRF）。
- **列表**：`GET` 列表支援 `page`（從 1 起）、`page_size`（預設 50，上限 500），回傳 `{ "items": [...], "total": 123 }`。
- **錯誤**：HTTP 狀態碼 + `{ "error": { "code": "not_found", "message": "裝置不存在" } }`。常見碼：`bad_request`、`unauthorized`、`forbidden`、`not_found`、`conflict`、`rate_limited`、`internal`。
- **角色**：`admin` > `operator` > `helpdesk` > `viewer`。每個端點標註最低角色。
- **目標（Target）物件**：用於指令批次、部署、排程。

```json
{ "all": false, "device_ids": ["uuid"], "group_ids": ["uuid"], "tags": ["財務部"] }
```

## 1. 驗證

| 方法 | 路徑 | 角色 | 說明 |
|---|---|---|---|
| POST | `/auth/login` | 公開 | `{username, password}` → `User`。AD 帳號可用 `user`、`DOMAIN\user` 或 `user@domain`。 |
| POST | `/auth/logout` | 任何 | 撤銷工作階段 |
| GET | `/auth/me` | 任何 | 目前使用者 `User` |
| POST | `/auth/password` | 本機帳號 | `{current_password, new_password}` |

```json
// User
{ "id": "uuid", "username": "admin", "display_name": "系統管理員", "email": "",
  "source": "local|ad", "role": "admin|operator|helpdesk|viewer", "disabled": false,
  "last_login_at": "2026-10-17T00:00:00Z", "created_at": "..." }
```

## 2. 儀表板

`GET /dashboard` （viewer）

```json
{
  "devices": { "total": 120, "online": 98, "offline": 22, "with_agent": 110, "without_agent": 10 },
  "by_type": [{ "key": "windows", "count": 110 }],
  "by_os_edition": [{ "key": "Windows 11 Pro", "count": 80 }],
  "by_lifecycle": [{ "key": "in_use", "count": 100 }],
  "health": { "ok": 90, "warning": 15, "critical": 5, "unknown": 10 },
  "battery_alerts": [{ "device_id": "uuid", "device_name": "NB-001", "health_percent": 62.5 }],
  "warranty_expiring": [{ "device_id": "uuid", "device_name": "PC-01", "warranty_end": "2026-11-30" }],
  "top_issues": [{ "code": "disk_low", "severity": "warning", "count": 7 }],
  "recent_commands": [Command],
  "unread_chats": 3
}
```

## 3. 裝置

| 方法 | 路徑 | 角色 | 說明 |
|---|---|---|---|
| GET | `/devices` | viewer | 查詢參數：`q`（名稱/IP/序號/使用者模糊搜尋）、`status`（online/offline）、`type`、`lifecycle`、`health`、`group_id`、`tag`、`has_agent`（true/false）、`sort`（name/last_seen/-last_seen…） |
| GET | `/devices/{id}` | viewer | `DeviceDetail` |
| POST | `/devices` | operator | 手動新增非 Agent 設備（印表機等）：`{name, device_type, ip_addresses, mac_addresses, ...}` |
| POST | `/devices/bulk` | operator | 批次調整：`{device_ids, add_tags?, group_id?}`（`group_id: ""` 代表移出群組） |
| PATCH | `/devices/{id}` | operator | 可改：`name, group_id, tags, asset_tag, owner_name, department, location, purchase_date, warranty_end, notes, device_type` |
| DELETE | `/devices/{id}` | admin | 刪除裝置紀錄（Agent 會收到解除註冊） |
| POST | `/devices/{id}/lifecycle` | operator | `{state, reason}` 變更生命週期 |
| GET | `/devices/{id}/lifecycle` | viewer | `LifecycleEvent[]` |
| GET | `/devices/{id}/inventory` | viewer | `{ hardware: Hardware \| null, hardware_collected_at, software_collected_at, software_count }` |
| GET | `/devices/{id}/software` | viewer | `{items: Software[], total}`，支援 `q` |
| GET | `/devices/{id}/inventory/changes` | viewer | `InventoryChange[]` |
| GET | `/devices/{id}/health` | viewer | `{ latest: Health \| null, history: [{collected_at, cpu_percent, mem_percent, status}] }`，`days`（預設 7） |
| GET | `/devices/{id}/battery` | viewer | `{ latest: Battery \| null, history: [{collected_at, health_percent, full_charge_capacity_mwh, design_capacity_mwh, cycle_count}] }`，`days`（預設 180） |
| GET | `/devices/{id}/commands` | viewer | `{items: Command[], total}` |
| POST | `/devices/{id}/commands` | helpdesk | `NewCommand` → `Command`（helpdesk 只可 `refresh`、`notify`、`lock`） |
| GET | `/devices/{id}/messages` | helpdesk | `ChatMessage[]`（最近 200 筆，`before` 參數分頁） |
| POST | `/devices/{id}/messages` | helpdesk | `{body}` → `ChatMessage` |
| POST | `/devices/{id}/messages/read` | helpdesk | 將使用者送來的訊息標為已讀 |
| POST | `/devices/{id}/remote` | helpdesk | `{mode: "view"\|"control", reason}` → `RemoteSession` |
| GET | `/devices/{id}/remote-sessions` | helpdesk | 此裝置的遠端連線紀錄 |

```json
// Device（列表項目）
{ "id": "uuid", "name": "PC-ACC-01", "hostname": "PC-ACC-01", "domain": "corp.local",
  "device_type": "windows|server|printer|network|storage|other|unknown",
  "sources": ["agent","ad","discovery","manual"], "has_agent": true,
  "online": true, "last_seen_at": "...", "os_name": "Windows 11 Pro", "os_version": "10.0.22631",
  "os_edition": "Pro", "manufacturer": "Dell Inc.", "model": "Latitude 5440", "serial_number": "ABC123",
  "chassis_type": "laptop", "ip_addresses": ["10.0.1.23"], "mac_addresses": ["00:11:22:33:44:55"],
  "logged_in_user": "CORP\\alice", "group_id": "uuid|null", "group_name": "財務部",
  "tags": ["財務部"], "lifecycle_state": "procurement|in_stock|in_use|repair|idle|retired",
  "health_status": "ok|warning|critical|unknown", "issue_count": 2,
  "cpu_percent": 12.5, "mem_percent": 61.2, "battery_health": 87.3,
  "agent_version": "1.0.0", "asset_tag": "TG-2024-001", "owner_name": "Alice", "department": "財務部",
  "location": "台北 5F", "purchase_date": "2024-01-15", "warranty_end": "2027-01-14",
  "notes": "", "ad_dn": "CN=PC-ACC-01,OU=...", "ad_enabled": true, "ad_last_logon": "...",
  "created_at": "...", "updated_at": "..." }

// DeviceDetail = Device + { "issues": Issue[], "uptime_sec": 3600, "pending_reboot": false,
//   "helper_online": true, "consent_policy": "always_ask", "enrolled_at": "...", "first_seen_at": "..." }

// Issue
{ "code": "disk_low", "severity": "info|warning|critical", "message": "C: 剩餘 6%" }

// NewCommand
{ "kind": "powershell|cmd|reboot|shutdown|refresh|notify|lock|logoff|service.restart|process.kill|uninstall",
  "script": "Get-Process",       // powershell / cmd
  "timeout_sec": 300,
  "args": { ... }                 // 依 kind：reboot → {force, countdown_sec, message, allow_defer}
}                                 //         notify → {title, message, level}

// Command
{ "id": "uuid", "device_id": "uuid", "device_name": "PC-01", "kind": "powershell",
  "args": {...}, "status": "queued|sent|running|succeeded|failed|timeout|canceled|expired|rejected",
  "exit_code": 0, "stdout": "...", "stderr": "", "truncated": false,
  "issued_by_name": "admin", "created_at": "...", "started_at": "...", "finished_at": "...",
  "deployment_id": null, "schedule_id": null }

// ChatMessage
{ "id": "uuid", "device_id": "uuid", "direction": "mis|user", "sender": "admin",
  "body": "您好", "created_at": "...", "delivered_at": "...", "read_at": null }

// RemoteSession
{ "id": "uuid", "device_id": "uuid", "mode": "view|control",
  "status": "pending|active|denied|ended|failed", "requester_name": "admin", "reason": "...",
  "consent_user": "CORP\\alice", "created_at": "...", "started_at": null, "ended_at": null, "end_reason": "" }
```

### 指令批次

| 方法 | 路徑 | 角色 | 說明 |
|---|---|---|---|
| POST | `/commands/batch` | operator | `{target: Target, command: NewCommand}` → `{count, command_ids}` |
| GET | `/commands` | viewer | 全部指令紀錄，`status`、`kind`、`device_id` 過濾 |
| GET | `/commands/{id}` | viewer | `Command` |
| POST | `/commands/{id}/cancel` | operator | 取消尚未執行的指令 |

## 4. 遠端連線

| 方法 | 路徑 | 角色 | 說明 |
|---|---|---|---|
| GET | `/remote-sessions` | helpdesk | 連線紀錄 |
| GET | `/remote-sessions/{id}` | helpdesk | `RemoteSession` |
| POST | `/remote-sessions/{id}/stop` | helpdesk | 結束連線 |
| WS | `/remote-sessions/{id}/ws` | helpdesk | 檢視端媒體通道（見下） |

### 檢視端 WebSocket 協定

- 伺服器 → 瀏覽器：二進位訊框，第 1 個位元組為類型（大端序）：
  - `0x01` 螢幕資訊：其後為 UTF-8 JSON `{"width":1920,"height":1080,"monitor":0,"monitors":2}`
  - `0x02` 畫面區塊：`u16 x, u16 y, u16 w, u16 h` + JPEG 位元組
  - `0x03` 一幀結束：`u32 seq`（瀏覽器收到後回 `ack`）
  - `0x04` 游標位置：`u16 x, u16 y`
- 伺服器 → 瀏覽器：文字訊框 JSON `{"t":"status","status":"waiting_consent|active|ended|denied","reason":"..."}`
- 瀏覽器 → 伺服器：文字訊框 JSON
  - `{"t":"ack","seq":12}`
  - `{"t":"mouse","a":"move|down|up|wheel","x":100,"y":200,"b":0,"d":-120}`（b：0 左 1 中 2 右；座標為遠端螢幕像素）
  - `{"t":"key","a":"down|up","code":"KeyA","key":"a"}`（code 為 `KeyboardEvent.code`）
  - `{"t":"text","s":"要輸入的文字"}`
  - `{"t":"quality","q":60}`（10–90）
  - `{"t":"monitor","i":1}`
  - `{"t":"cad"}`（傳送 Ctrl+Alt+Del）

## 5. 即時事件

`WS /events/ws`（viewer）伺服器推播 `{ "type": "...", "data": {...} }`：

| type | data |
|---|---|
| `device.online` / `device.offline` | `{device_id, name}` |
| `device.updated` | `Device` |
| `command.updated` | `Command` |
| `chat.message` | `ChatMessage` |
| `remote.updated` | `RemoteSession` |
| `deployment.updated` | `Deployment` |
| `scan.updated` | `DiscoveryScan` |

## 6. 裝置群組

| 方法 | 路徑 | 角色 |
|---|---|---|
| GET | `/groups` | viewer |
| POST | `/groups` | admin：`{name, description, consent_policy}` |
| PATCH | `/groups/{id}` | admin |
| DELETE | `/groups/{id}` | admin |

```json
// Group
{ "id": "uuid", "name": "財務部", "description": "", "consent_policy": "always_ask|notify_only|deny", "device_count": 12 }
```

## 7. 設備探索

| 方法 | 路徑 | 角色 | 說明 |
|---|---|---|---|
| GET | `/discovery/scans` | viewer | |
| POST | `/discovery/scans` | operator | `{cidrs: ["10.0.1.0/24"], ports?: [..], probe_device_id?: "uuid"}`（指定 Agent 代為掃描） |
| GET | `/discovery/scans/{id}` | viewer | `DiscoveryScan` + `hosts: DiscoveredHost[]` |
| POST | `/discovery/hosts/import` | operator | `{scan_id, ips: [...]}` 將探索結果登錄為裝置 |

```json
// DiscoveryScan
{ "id": "uuid", "cidrs": ["10.0.1.0/24"], "status": "running|completed|failed", "found": 34,
  "requested_by_name": "admin", "probe_device_name": null, "started_at": "...", "finished_at": "...", "error": "" }
// DiscoveredHost
{ "ip": "10.0.1.50", "mac": "...", "hostname": "printer-5f", "open_ports": [80, 9100],
  "vendor": "HP", "device_type": "printer", "device_id": "uuid|null", "rtt_ms": 2 }
```

## 8. 軟體套件與部署

| 方法 | 路徑 | 角色 | 說明 |
|---|---|---|---|
| GET | `/packages` | viewer | |
| POST | `/packages` | operator | multipart：`file` + `name, version, publisher, type(msi/exe/msix/ps1), install_args, uninstall_args, success_codes(逗號分隔)` |
| PATCH | `/packages/{id}` | operator | |
| DELETE | `/packages/{id}` | admin | |
| GET | `/deployments` | viewer | |
| POST | `/deployments` | operator | `{name, package_id, target: Target, scheduled_at?: "..."}` |
| GET | `/deployments/{id}` | viewer | `Deployment` + `results: [{device_id, device_name, status, exit_code, finished_at, command_id}]` |
| POST | `/deployments/{id}/cancel` | operator | |
| POST | `/deployments/{id}/retry` | operator | 重試失敗的目標 |

```json
// Package
{ "id": "uuid", "name": "7-Zip", "version": "24.08", "publisher": "Igor Pavlov", "file_name": "7z.msi",
  "file_size": 1572864, "sha256": "...", "type": "msi", "install_args": "/qn /norestart",
  "uninstall_args": "", "success_codes": [0,3010,1641], "created_by_name": "admin", "created_at": "..." }
// Deployment
{ "id": "uuid", "name": "部署 7-Zip", "package_id": "uuid", "package_name": "7-Zip 24.08",
  "target": Target, "status": "scheduled|running|completed|canceled", "scheduled_at": null,
  "total": 50, "succeeded": 45, "failed": 2, "pending": 3, "created_by_name": "admin", "created_at": "..." }
```

## 9. 指令庫與排程

| 方法 | 路徑 | 角色 |
|---|---|---|
| GET/POST | `/scripts` | viewer / operator：`{name, description, shell: "powershell\|cmd", body, timeout_sec}` |
| PATCH/DELETE | `/scripts/{id}` | operator |
| GET/POST | `/schedules` | viewer / operator |
| PATCH/DELETE | `/schedules/{id}` | operator |
| POST | `/schedules/{id}/run` | operator：立即執行一次 |

```json
// Schedule
{ "id": "uuid", "name": "每週清除暫存", "cron": "0 3 * * 1", "enabled": true,
  "action": { "type": "command", "command": NewCommand }   // 或 {"type":"deployment","package_id":"uuid"}
                                                            // 或 {"type":"refresh"}
  , "target": Target, "last_run_at": null, "next_run_at": "...", "created_by_name": "admin" }
```

Cron 為標準 5 欄位（分 時 日 月 週），時區以伺服器設定的 `timezone`（預設 Asia/Taipei）為準。

## 10. 稽核

`GET /audit`（admin）參數：`actor`、`action`、`target_id`、`from`、`to`、`q`

```json
{ "id": 1, "at": "...", "actor_name": "admin", "actor_ip": "10.0.0.5", "action": "command.create",
  "target_type": "device", "target_id": "uuid", "target_name": "PC-01",
  "details": {...}, "result": "success|failure" }
```

`GET /audit/export?format=csv`（admin）匯出 CSV。

## 11. 系統設定（admin）

| 方法 | 路徑 | 說明 |
|---|---|---|
| GET/PUT | `/settings/ad` | `ADSettings`（`bind_password` 只寫不讀，回傳 `bind_password_set: true`） |
| POST | `/settings/ad/test` | 測試連線，回傳 `{ok, message, sample_computers}` |
| POST | `/settings/ad/sync` | 立即同步電腦物件，回傳 `{added, updated, total}` |
| GET/PUT | `/settings/agent` | `AgentConfig`（心跳、盤點頻率、同意政策、重開機倒數…） |
| GET/PUT | `/settings/general` | `{ server_url, timezone, data_retention_days, battery_alert_percent, warranty_alert_days }` |
| GET | `/enrollment-tokens` | |
| POST | `/enrollment-tokens` | `{name, expires_at?, max_uses?, group_id?, tags?}` → 回傳 `EnrollmentToken` 加上 `token`（明文，只顯示一次）與 `install_command`（可直接貼到系統管理員 PowerShell 的單行安裝指令） |
| DELETE | `/enrollment-tokens/{id}` | 撤銷 |
| GET/POST | `/users` | |
| PATCH/DELETE | `/users/{id}` | `{display_name, email, role, disabled, password?}` |
| GET | `/system/info` | `{version, build_time, go_version, db_version, protocol, agents_connected, uptime_sec, agent_latest_version, agent_installer_available}` |
| GET | `/agent/download` | 下載目前版本 Agent 安裝檔（viewer） |

```json
// ADSettings
{ "enabled": true, "url": "ldaps://dc01.corp.local:636", "base_dn": "DC=corp,DC=local",
  "bind_dn": "CN=svc-nexmis,OU=Service,DC=corp,DC=local", "bind_password_set": true,
  "computer_base_dn": "OU=Computers,DC=corp,DC=local", "user_base_dn": "DC=corp,DC=local",
  "domain": "corp.local", "netbios": "CORP", "insecure_skip_verify": false, "ca_cert_pem": "",
  "sync_interval_min": 360, "role_mappings": [{ "group_dn": "CN=NEXMIS-Admins,...", "role": "admin" }] }

// AgentConfig
{ "heartbeat_sec": 60, "health_sec": 900, "battery_sec": 21600, "hardware_sec": 86400,
  "software_sec": 21600, "consent_policy": "always_ask", "consent_timeout_sec": 60,
  "reboot_countdown_sec": 300, "reboot_max_defer": 3 }

// EnrollmentToken
{ "id": "uuid", "name": "總部大量部署", "token_prefix": "nxe_ab12", "expires_at": null, "max_uses": null,
  "uses": 12, "group_id": null, "tags": [], "revoked": false, "created_by_name": "admin", "created_at": "..." }
```

## 12. Agent 端點（僅供 Agent 使用）

| 方法 | 路徑 | 說明 |
|---|---|---|
| POST | `/api/agent/v1/enroll` | 註冊，見 `protocol.EnrollRequest` |
| WS | `/api/agent/v1/ws` | 控制通道 |
| WS | `/api/agent/v1/remote/{session_id}?token=` | 使用者端助手的媒體通道 |
| GET | `/api/agent/v1/files/{token}` | HMAC 簽章下載權杖（部署套件、Agent 更新），綁定裝置且有期限，支援 Range 續傳 |
| GET | `/api/agent/v1/installer?arch=` | 以 `X-NEXMIS-Enroll-Token` 標頭帶入有效註冊權杖（不放在網址，避免出現在代理或存取記錄）下載 Agent 執行檔（不消耗權杖次數，限速），供 `install-agent.ps1` 與單行安裝指令使用 |
| GET | `/healthz` | 存活檢查（公開） |
