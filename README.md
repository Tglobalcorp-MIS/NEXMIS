# NEXMIS 企業 MIS 整合管理平台

NEXMIS 是一套自建、可離線運作的企業 IT 資產與端點管理平台，由三個元件組成：

| 元件 | 說明 |
|---|---|
| **nexmis-server** | 中央管理服務（Go）。內嵌 Web 管理端、REST API、Agent 閘道、排程、部署、探索、AD 同步、遠端連線中繼與稽核。單一執行檔＋PostgreSQL。 |
| **Web 管理端** | React + Ant Design 單頁應用，建置後內嵌於 nexmis-server，不需另外架設網站。 |
| **nexmis-agent** | Windows Agent（Go，原生執行檔）。以 Windows 服務執行，另在使用者桌面工作階段啟動「使用者端助手」處理遠端畫面、同意授權、訊息與重開機提示。支援 Windows 10/11 家用版、專業版與 Windows Server。 |

## 功能

- **設備探索**：依網段（CIDR）以 TCP 連線、NetBIOS、ARP 掃描，辨識廠牌（OUI）與設備類型；可由伺服器或指定的 Agent（跨 VLAN）執行，結果可一鍵匯入資產。
- **硬體／軟體盤點**：CPU、記憶體、磁碟、網卡、BIOS、TPM、BitLocker、顯示卡、已安裝軟體與更新；差異上傳並保留異動紀錄。
- **筆電電池健康**：設計容量、完整充電容量、循環次數、健康度趨勢與低於門檻警示。
- **設備健康診斷**：CPU／記憶體／磁碟、開機時間、待重開機、Defender、防火牆、服務、事件記錄錯誤，自動產生問題清單與嚴重度。
- **遠端桌面與畫面分享**：僅檢視或可控制兩種模式，經伺服器中繼（端點無需開放入站埠），不依賴 RDP，Windows 家用版亦可使用。
- **使用者連線同意**：依政策（一律詢問／僅通知不需同意／禁止遠端）在使用者桌面跳出同意視窗，逾時或無人登入視為拒絕；連線期間桌面持續顯示提示並可隨時中止。
- **雙向即時訊息**：MIS 與使用者之間即時對話，含已送達／已讀狀態。
- **安全重新開機**：在使用者桌面倒數提示，允許使用者延後（可設定次數上限），無人登入時直接執行。
- **遠端指令**：PowerShell／CMD（可選以登入使用者身分執行）、服務重啟、結束處理程序、鎖定、登出、關機；所有指令由伺服器簽章、Agent 驗章後才執行。
- **軟體部署**：上傳 MSI／EXE／MSIX 等套件（SHA-256 校驗），以群組／標籤／裝置為目標派送，可續傳、可重試、自訂成功代碼。
- **設備生命週期**：採購、庫存、使用中、維修、閒置、報廢；保固到期提醒、資產編號、保管人、部門、位置。
- **完整稽核**：所有登入與操作皆寫入不可修改的稽核紀錄（資料庫觸發器保護），可匯出 CSV。
- **Active Directory 整合**：LDAPS 唯讀查詢、AD 帳號登入（群組對應角色、支援巢狀群組）、定期同步電腦物件。
- **角色權限**：admin、operator、helpdesk、viewer 四級。
- **Agent 自動更新、自動重連、離線暫存**。

## 快速開始（Docker）

```bash
cd deploy/docker
cp .env.example .env          # 填入 SERVER_NAME、PUBLIC_URL、DB_PASSWORD、MASTER_KEY、INITIAL_ADMIN_PASSWORD
mkdir certs && cp /path/to/server.crt /path/to/server.key certs/
docker compose up -d --build
```

開啟 `https://<SERVER_NAME>`，以 `admin` 與 `INITIAL_ADMIN_PASSWORD` 登入，接著：

1. 「系統設定 → 一般」確認伺服器網址。
2. 「系統設定 → Active Directory」設定 LDAPS 與唯讀服務帳號（選用）。
3. 「系統設定 → 註冊權杖」建立權杖，複製單行安裝指令，在電腦上以系統管理員 PowerShell 執行；或以 GPO 開機指令碼執行 `install-agent.ps1` 大量部署。

完整步驟請見 **[部署指南](docs/deployment.md)**。

## 文件

| 文件 | 內容 |
|---|---|
| [docs/deployment.md](docs/deployment.md) | 部署：Docker、Linux systemd、Windows Server、TLS、AD 設定、Agent 大量部署、防火牆 |
| [docs/operations.md](docs/operations.md) | 維運交接：備份還原、金鑰保管、升級、監控、疑難排解、已知限制、實機驗證清單 |
| [docs/architecture.md](docs/architecture.md) | 系統架構、安全設計、協定、AI 擴充介面 |
| [docs/api.md](docs/api.md) | REST API 與 Agent 協定 |

## 開發

需求：Go 1.26、Node.js 22、PostgreSQL 16。

```bash
# 後端（含整合測試）
export NEXMIS_TEST_DATABASE_URL=postgres://nexmis:nexmis@localhost:5432/nexmis_test?sslmode=disable
go test ./...
GOOS=windows go vet ./...

# 前端開發伺服器（代理到 localhost:8080）
cd web && npm ci && npm run dev

# 本機啟動伺服器
NEXMIS_INITIAL_ADMIN_PASSWORD='ChangeMe-2026!' go run ./cmd/nexmis-server

# 正式建置（web + 伺服器 + Agent + manifest）
VERSION=1.0.0 scripts/build.sh
```

### 程式結構

```
cmd/nexmis-server      中央服務進入點
cmd/nexmis-agent       Agent 進入點（服務、安裝、使用者端助手）
internal/protocol      Agent ⇄ 伺服器共用協定與簽章
internal/server/...    api、core（業務邏輯）、store（PostgreSQL）、ad、auth、secrets、events、config
internal/agent/...     agent（連線與指令）、collect（盤點/健康）、helper（使用者端助手）、
                       helperhost、helperipc、sys、winsvc、tlsconf、config
internal/discovery     網段掃描、NetBIOS、ARP、OUI
web/                   Web 管理端（React + Ant Design）
deploy/                Docker、systemd、Agent 安裝指令碼
docs/                  文件
```

## 授權

公司內部專案，未經授權不得散布。
