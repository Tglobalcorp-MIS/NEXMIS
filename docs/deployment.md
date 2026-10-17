# NEXMIS 部署指南

> 適用版本：1.x　對象：負責安裝與維運 NEXMIS 的 MIS 人員

## 1. 部署前規劃

### 1.1 主機需求

| 規模（端點數） | CPU | 記憶體 | 磁碟 | 備註 |
|---|---|---|---|---|
| ≤ 300 | 2 vCPU | 4 GB | 40 GB SSD | 伺服器與 PostgreSQL 同機 |
| 300–2,000 | 4 vCPU | 8 GB | 100 GB SSD | 建議 PostgreSQL 獨立磁碟 |
| 2,000–5,000 | 8 vCPU | 16 GB | 200 GB SSD | PostgreSQL 可獨立主機 |

磁碟用量主要來自軟體部署套件（存放於資料目錄）與健康／電池歷史（依「資料保留天數」自動清除，預設 90 天）。

作業系統建議：Ubuntu 24.04 LTS／Debian 12／RHEL 9（Docker 或 systemd），或 Windows Server 2019 以上（原生服務）。

### 1.2 與 AD 網域控制站的隔離原則

- **不要**把 NEXMIS 安裝在網域控制站上，也不要與 DC 共用主機或資料庫。
- NEXMIS 只以**唯讀服務帳號**透過 **LDAPS（636）** 查詢 AD；不寫入 AD、不修改 GPO、不需要 Domain Admin。
- 伺服器主機可加入網域，也可不加入；兩者皆可運作。
- 若停用 AD 整合，NEXMIS 仍可以本機帳號完整運作，AD 故障不影響端點管理。

### 1.3 網路與防火牆

| 來源 → 目的 | 埠 | 用途 |
|---|---|---|
| 端點（Agent）→ NEXMIS | TCP 443（或自訂） | Agent 控制通道（WSS）、下載更新／套件、遠端畫面中繼 |
| MIS 人員瀏覽器 → NEXMIS | TCP 443 | Web 管理端 |
| NEXMIS → 網域控制站 | TCP 636 | LDAPS 唯讀查詢 |
| NEXMIS → 被掃描網段 | 掃描設定的 TCP 埠、UDP 137 | 設備探索（伺服器端掃描時） |

- 端點**不需要開放任何入站埠**；所有連線都由 Agent 主動連出。
- 跨 VLAN 或伺服器無法直接觸及的網段，可在探索時指定「由某台 Agent 代為掃描」。
- 以 Docker 部署時，容器位於橋接網路，伺服器端掃描無法取得 ARP／MAC 位址；建議以 Agent 代為掃描，或改用 systemd 原生部署。

### 1.4 TLS 憑證

Agent 一律透過 HTTPS／WSS 連線。三種做法擇一：

1. **企業 CA 簽發（建議）**：由公司 AD CS 或其他內部 CA 簽發 `mis.corp.local` 憑證。網域電腦已信任企業根憑證，Agent 不需額外參數。
2. **企業 CA，但端點未信任**：安裝 Agent 時加上 `--ca ca.pem`（或 `install-agent.ps1 -CAFile`）。
3. **自簽憑證**：安裝 Agent 時加上 `--pin <憑證 SHA-256 指紋>`。取得指紋：
   ```bash
   openssl x509 -in server.crt -noout -fingerprint -sha256 | cut -d= -f2 | tr -d :
   ```

> 憑證主體名稱（SAN）必須與 `PUBLIC_URL` 的主機名稱一致。更換憑證時：方案 1、2 不需動作；方案 3 必須先更新所有 Agent 的指紋（見維運文件）。

## 2. 安裝方式 A：Docker Compose（建議）

```bash
git clone <repo> nexmis && cd nexmis/deploy/docker
cp .env.example .env
openssl rand -base64 24   # → DB_PASSWORD
openssl rand -base64 32   # → MASTER_KEY
vi .env                   # 填入 SERVER_NAME、PUBLIC_URL、DB_PASSWORD、MASTER_KEY、INITIAL_ADMIN_PASSWORD
mkdir -p certs && cp /path/server.crt /path/server.key certs/   # server.crt 需包含完整憑證鏈
docker compose up -d --build
docker compose logs -f server
```

- 架構：`proxy`（Caddy，終結 TLS）→ `server`（nexmis-server）→ `db`（PostgreSQL 16，僅位於內部網路，不對外開放）。
- Agent 執行檔隨映像檔一起發佈（`/opt/nexmis/agent`），**升級映像檔即自動發佈新版 Agent**，已安裝的 Agent 會自動更新。
- 資料：`db` volume（資料庫）、`data` volume（部署套件、伺服器簽章金鑰）。兩者都要備份。
- `INITIAL_ADMIN_PASSWORD` 只在第一次啟動時使用；若留空，伺服器會在日誌中印出隨機產生的密碼。

## 3. 安裝方式 B：Linux systemd（原生）

```bash
# 在建置機上
VERSION=1.0.0 scripts/build.sh            # 產出 dist/

# 在伺服器上（需先安裝 PostgreSQL 16）
sudo -u postgres psql -c "CREATE USER nexmis WITH PASSWORD '<密碼>';"
sudo -u postgres psql -c "CREATE DATABASE nexmis OWNER nexmis;"

sudo dist/linux/install.sh dist
sudo vi /etc/nexmis/nexmis.env            # 資料庫連線、PUBLIC_URL、TLS 憑證、初始密碼
sudo systemctl enable --now nexmis-server
journalctl -u nexmis-server -f
```

- 服務以 `nexmis` 系統帳號執行，套用 systemd 沙箱強化設定；資料目錄 `/var/lib/nexmis`。
- 啟動前會自動執行資料庫結構升級（`--migrate`）。
- 若前面另有 nginx 等反向代理終結 TLS，請設定 `NEXMIS_TRUST_PROXY=true`，並確保代理支援 WebSocket、不設讀取逾時、上傳上限至少 2 GB。

## 4. 安裝方式 C：Windows Server（原生服務）

1. 安裝 PostgreSQL 16（Windows 版），建立 `nexmis` 使用者與資料庫。
2. 將 `nexmis-server-windows-amd64.exe` 複製到 `C:\Program Files\NEXMIS\Server\nexmis-server.exe`，`dist\agent\` 整個資料夾複製到 `C:\ProgramData\NEXMIS\Server\agent\`。
3. 建立 `C:\ProgramData\NEXMIS\Server\nexmis.env`：
   ```ini
   NEXMIS_LISTEN=:443
   NEXMIS_PUBLIC_URL=https://mis.corp.local
   NEXMIS_TLS_CERT=C:\ProgramData\NEXMIS\Server\tls\server.crt
   NEXMIS_TLS_KEY=C:\ProgramData\NEXMIS\Server\tls\server.key
   NEXMIS_DATABASE_URL=postgres://nexmis:<密碼>@127.0.0.1:5432/nexmis?sslmode=disable
   NEXMIS_DATA_DIR=C:\ProgramData\NEXMIS\Server\data
   NEXMIS_AGENT_DIR=C:\ProgramData\NEXMIS\Server\agent
   NEXMIS_INITIAL_ADMIN_PASSWORD=<初始密碼>
   ```
4. 以系統管理員身分執行：
   ```powershell
   & 'C:\Program Files\NEXMIS\Server\nexmis-server.exe' --install-service --config C:\ProgramData\NEXMIS\Server\nexmis.env
   New-NetFirewallRule -DisplayName 'NEXMIS HTTPS' -Direction Inbound -Protocol TCP -LocalPort 443 -Action Allow
   ```
   服務名稱 `NexmisServer`，失敗會自動重啟；日誌位於 `C:\ProgramData\NEXMIS\Server\logs\nexmis-server.log`。
5. 移除服務：`nexmis-server.exe --uninstall-service`。
6. 請以 NTFS 權限限制 `C:\ProgramData\NEXMIS\Server` 只允許 SYSTEM 與 Administrators 存取（內含主金鑰與簽章金鑰）。

## 5. 首次設定

1. 以 `admin` 登入，立即至右上角「變更密碼」。
2. **系統設定 → 一般**：確認「伺服器網址」與 `PUBLIC_URL` 一致（安裝指令與下載連結會使用此網址）、時區、資料保留天數、電池與保固警示門檻。
3. **系統設定 → Agent 政策**：心跳、盤點頻率、遠端同意政策、重開機倒數與可延後次數。
4. **系統設定 → 使用者**：建立其他本機帳號，或改用 AD 登入（下一節）。
5. **裝置群組**：依部門或地點建立群組，供部署與批次指令使用。

## 6. Active Directory 整合

### 6.1 在 AD 建立唯讀服務帳號

```powershell
New-ADUser -Name 'svc-nexmis' -SamAccountName 'svc-nexmis' -Path 'OU=Service Accounts,DC=corp,DC=local' `
  -AccountPassword (Read-Host -AsSecureString '密碼') -Enabled $true -PasswordNeverExpires $true `
  -CannotChangePassword $true -Description 'NEXMIS 唯讀查詢'
```

- 一般「Domain Users」權限即可讀取電腦與使用者物件，**不需要**任何額外權限。
- 建議在 GPO「拒絕本機登入／拒絕透過遠端桌面登入」中加入此帳號。

### 6.2 建立對應角色的安全群組

例如 `NEXMIS-Admins`、`NEXMIS-Operators`、`NEXMIS-Helpdesk`、`NEXMIS-Viewers`。支援巢狀群組。使用者登入時依群組決定角色，不屬於任何對應群組的 AD 帳號無法登入。

### 6.3 在 NEXMIS 設定

**系統設定 → Active Directory**：

| 欄位 | 範例 |
|---|---|
| 伺服器 URL | `ldaps://dc01.corp.local:636`（可多台以空白分隔，依序容錯） |
| 網域 | `corp.local`、NetBIOS `CORP` |
| Base DN | `DC=corp,DC=local` |
| 服務帳號 DN | `CN=svc-nexmis,OU=Service Accounts,DC=corp,DC=local` |
| 電腦 OU | `OU=Workstations,DC=corp,DC=local`（同步範圍） |
| CA 憑證 | DC 憑證不受伺服器信任時貼上企業根憑證 PEM |
| 角色對應 | 群組 DN → 角色 |
| 同步間隔 | 360 分鐘 |

按「測試連線」確認可列出電腦後儲存，再按「立即同步」。AD 中的電腦會以「來源：AD」出現在設備清單，Agent 註冊後自動依主機名稱合併。

## 7. 部署 Agent

### 7.1 建立註冊權杖

**系統設定 → 註冊權杖 → 新增**：可設定到期日、使用次數上限、預設群組與標籤。明文權杖只顯示一次，請妥善保存；權杖外洩時立即撤銷即可，已註冊的 Agent 不受影響（Agent 註冊後改用各自的金鑰）。

### 7.2 單台安裝

複製權杖畫面上的單行指令，在目標電腦以**系統管理員 PowerShell** 執行。

### 7.3 GPO 大量部署（網域電腦）

1. 將 `install-agent.ps1`（位於 `dist/agent/`，或從伺服器資料目錄取得）與 `nexmis-agent-windows-amd64.exe` 放到 `\\corp.local\NETLOGON\nexmis\`。
2. 建立 GPO →「電腦設定 → 原則 → Windows 設定 → 指令碼 → 啟動」→ PowerShell 指令碼：
   - 指令碼：`\\corp.local\NETLOGON\nexmis\install-agent.ps1`
   - 參數：`-Server https://mis.corp.local -Token nxe_xxxxx -AgentPath \\corp.local\NETLOGON\nexmis\nexmis-agent-windows-amd64.exe`
3. 連結到目標 OU。電腦下次開機即安裝；已安裝者會略過重新註冊，只更新執行檔與服務設定。
4. 安裝記錄：`C:\ProgramData\NEXMIS\install.log`。

### 7.4 非網域電腦（Windows 家用版等）

使用單行指令或 `install-agent.ps1`（不帶 `-AgentPath`，自動從伺服器下載）。若伺服器使用企業 CA 而該電腦未信任，加上 `-CAFile` 或 `-PinSHA256`。

### 7.5 Intune／其他派送工具

以 SYSTEM 身分執行：

```
powershell.exe -NoProfile -ExecutionPolicy Bypass -File install-agent.ps1 -Server https://mis.corp.local -Token nxe_xxxxx
```

結束代碼 0 為成功，1 為失敗。

### 7.6 Agent 安裝位置

| 項目 | 位置 |
|---|---|
| 執行檔 | `C:\Program Files\NEXMIS\Agent\nexmis-agent.exe` |
| 設定與身分金鑰（DPAPI 加密） | `C:\ProgramData\NEXMIS\Agent\` |
| 日誌 | `C:\ProgramData\NEXMIS\Agent\logs\agent.log` |
| 服務 | `NexmisAgent`（LocalSystem，自動啟動，失敗自動重啟） |

本機指令：`nexmis-agent status`、`nexmis-agent collect`（輸出收集資料，除錯用）、`nexmis-agent uninstall [--purge]`。

## 8. 上線檢查清單

- [ ] HTTPS 憑證有效，瀏覽器無警告；`https://<host>/healthz` 回應 `ok`
- [ ] admin 已改密碼；至少建立一個備用 admin 帳號
- [ ] `master.key`／`MASTER_KEY` 與 `signing.key` 已備份至離線保管處（見維運文件）
- [ ] 資料庫每日備份排程已設定並完成一次還原演練
- [ ] AD 測試連線成功，以 AD 帳號登入確認角色正確
- [ ] 測試電腦安裝 Agent，確認：上線、盤點、健康、電池（筆電）、訊息、遠端（同意流程）、重新開機
- [ ] 稽核紀錄可查到上述操作
