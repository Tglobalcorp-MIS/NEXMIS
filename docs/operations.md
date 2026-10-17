# NEXMIS 維運交接手冊

> 對象：接手 NEXMIS 日常維運的 MIS 人員與後續開發者

## 1. 系統組成速查

| 項目 | Docker 部署 | Linux systemd | Windows Server |
|---|---|---|---|
| 服務 | `docker compose ps` | `systemctl status nexmis-server` | 服務 `NexmisServer` |
| 日誌 | `docker compose logs server` | `journalctl -u nexmis-server` | `C:\ProgramData\NEXMIS\Server\logs\` |
| 設定 | `deploy/docker/.env` | `/etc/nexmis/nexmis.env` | `C:\ProgramData\NEXMIS\Server\nexmis.env` |
| 資料目錄 | volume `data` | `/var/lib/nexmis` | `NEXMIS_DATA_DIR` |
| 資料庫 | volume `db` | PostgreSQL | PostgreSQL |

資料目錄內容：

| 檔案 | 用途 | 遺失的後果 |
|---|---|---|
| `master.key`（或環境變數 `NEXMIS_MASTER_KEY`） | 加密資料庫中的機密（AD 服務帳號密碼）與衍生下載權杖金鑰 | AD 密碼需重新輸入；尚未使用的下載連結失效 |
| `signing.key` | 伺服器 Ed25519 指令簽章私鑰，Agent 註冊時取得對應公鑰 | **所有 Agent 會拒絕新指令**，必須重新註冊每一台 Agent |
| `packages/` | 已上傳的部署套件 | 需重新上傳套件 |
| `agent/` | 發佈給端點的 Agent 執行檔與 `manifest.json`（Docker 版在映像檔內） | 新安裝與自動更新無法進行 |

## 2. 金鑰保管（最重要）

1. 上線當天將 `master.key` 與 `signing.key` 複製到**離線**媒體（加密隨身碟或保險櫃），並與資料庫備份分開保存。
2. 兩把金鑰都等同於「控制全公司電腦」的權限：`signing.key` 外洩者可偽造指令，請嚴格限制檔案權限（Linux `0600`／Windows 僅 SYSTEM 與 Administrators）。
3. 若懷疑 `signing.key` 外洩：停止服務 → 刪除 `signing.key` → 啟動（會產生新金鑰）→ 以 GPO 或指令碼對所有電腦執行 `nexmis-agent uninstall --purge` 後重新安裝。

## 3. 備份與還原

### 3.1 每日備份（範例：Docker）

```bash
#!/bin/bash
# /etc/cron.daily/nexmis-backup
set -euo pipefail
cd /opt/nexmis/deploy/docker
TS=$(date +%Y%m%d)
DEST=/backup/nexmis
mkdir -p $DEST
docker compose exec -T db pg_dump -U nexmis -Fc nexmis > $DEST/db-$TS.dump
docker run --rm -v nexmis_data:/data -v $DEST:/out alpine tar czf /out/data-$TS.tgz -C /data .
find $DEST -mtime +30 -delete
```

systemd 版本：`sudo -u postgres pg_dump -Fc nexmis > db.dump`，並備份 `/var/lib/nexmis`。

### 3.2 還原

```bash
docker compose stop server
docker compose exec -T db pg_restore -U nexmis -d nexmis --clean --if-exists < db-YYYYMMDD.dump
docker run --rm -v nexmis_data:/data -v /backup/nexmis:/in alpine sh -c 'rm -rf /data/* && tar xzf /in/data-YYYYMMDD.tgz -C /data'
docker compose start server
```

還原後 Agent 會自動重連；還原點之後才註冊的 Agent 需重新安裝。**每季至少演練一次還原。**

## 4. 升級

1. 先備份（第 3 節）。
2. Docker：`git pull && docker compose build && docker compose up -d`。
   systemd：以新版 `dist/` 重新執行 `install.sh`，然後 `systemctl restart nexmis-server`。
   Windows：停止服務 → 覆蓋執行檔與 `agent\` 資料夾 → 啟動服務。
3. 資料庫結構於啟動時自動升級（具備鎖定，多實例同時啟動也安全）。結構升級只會前進，降版請以備份還原。
4. 新版 Agent 隨伺服器發佈；`manifest.json` 的 `auto_update` 為 `true` 時，在線的 Agent 會自動下載、驗證 SHA-256 並更新。若要分批更新，可先將 `auto_update` 設為 `false`，再以「指令 → agent.update」對特定群組更新。
5. 升級後至「系統設定 → 系統資訊」確認版本，並抽查數台 Agent 版本。

## 5. 日常監控

| 項目 | 方法 |
|---|---|
| 服務存活 | 監控 `GET /healthz`（回應 200） |
| 在線 Agent 數 | 儀表板；或 `GET /api/v1/system/info` 的 `agents_connected` |
| 離線過久設備 | 設備清單篩選「離線」並依最後上線時間排序 |
| 健康問題 | 儀表板「問題設備」；設備清單篩選健康狀態 |
| 電池老化 | 儀表板電池警示（門檻於「系統設定 → 一般」） |
| 保固到期 | 儀表板保固警示 |
| 稽核 | 定期匯出稽核 CSV 歸檔；稽核紀錄保留天數獨立設定（預設 730 天，最少 180 天） |
| 磁碟 | 資料目錄（套件）與資料庫容量 |

## 6. 帳號管理

- 忘記 admin 密碼：
  - Docker：`docker compose exec server nexmis-server --reset-admin-password '<新密碼>'`
  - systemd：`sudo -u nexmis env NEXMIS_CONFIG=/etc/nexmis/nexmis.env nexmis-server --reset-admin-password '<新密碼>'`
  - Windows：`nexmis-server.exe --config <nexmis.env> --reset-admin-password "<新密碼>"`
- 連續 5 次登入失敗會鎖定帳號 15 分鐘。
- 密碼規則：至少 10 個字元，且包含大寫、小寫、數字、符號中的 3 種。
- AD 帳號的角色每次登入時依群組重新計算；從群組移除即失去權限。

## 7. 疑難排解

### 7.1 Agent 無法上線

1. 在端點執行 `nexmis-agent status`，查看服務狀態與最後錯誤。
2. 查看 `C:\ProgramData\NEXMIS\Agent\logs\agent.log`：
   - `x509: certificate signed by unknown authority`：端點不信任伺服器憑證，重新安裝並加 `--ca` 或 `--pin`。
   - `enroll: 401/403`：權杖無效、已撤銷、過期或達使用上限。
   - `authentication rejected`：裝置在伺服器端已被刪除或金鑰不符。註冊完成後 Agent 不保留權杖，請以新權杖重新執行 `nexmis-agent install --token <權杖>`（不需先移除）。
   - `agent is not enrolled and has no enrollment token`：同上，重新執行 install 並提供權杖。
3. 測試連線：`Test-NetConnection mis.corp.local -Port 443`。
4. Agent 斷線後會以 1 秒到 5 分鐘的指數退避自動重連，不需人工介入。

### 7.2 遠端連線失敗

| 結束原因 | 說明 |
|---|---|
| 使用者拒絕／逾時 | 使用者未同意 |
| 無使用者登入 | 端點目前沒有互動式登入的使用者（目前版本不支援登入畫面的遠端） |
| 政策禁止 | 「Agent 政策」設為禁止遠端 |
| 助手未啟動 | 使用者端助手未在使用者桌面執行；確認服務正在執行，必要時登出再登入 |

### 7.3 指令一直停在「已排入」

端點離線時指令會排隊，上線後送出；超過有效期限（依指令類型）會變成「已過期」。

### 7.4 部署失敗

在部署詳情查看每台的結束代碼與輸出。常見：1603（MSI 一般錯誤，查看 `C:\Windows\Temp` 的 MSI 記錄）、1618（另一個安裝正在進行，稍後重試）、3010（成功但需重新開機，已視為成功）。

### 7.5 AD 登入失敗

「系統設定 → Active Directory → 測試連線」。常見原因：DC 憑證不受信任（貼上 CA 憑證）、服務帳號密碼過期、使用者不屬於任何角色對應群組。

## 8. 已知限制（v1）

- **UAC 安全桌面與登入畫面**無法擷取或操作（Windows 安全設計）；遠端期間出現 UAC 提示時，請使用者自行點選，或改用遠端指令執行需要權限的操作。
- 遠端連線需要有使用者登入；無人登入的電腦請使用遠端指令。
- 不提供 MSI 安裝檔；以 `install-agent.ps1`／單行指令安裝（支援 GPO、Intune）。
- 每使用者安裝的軟體（HKCU）只在該使用者登入、登錄檔已載入時才會盤點到。
- 伺服器端探索掃描在 Docker 橋接網路下取不到 MAC 位址；請用 Agent 代為掃描。
- 單一伺服器實例設計（遠端連線中繼與 Agent 連線狀態在記憶體中）；高可用請以虛擬機快照／備份還原達成。
- Agent 只支援 Windows；其他設備（印表機、交換器、NAS）以探索與手動登錄管理。

## 9. Windows 實機驗證清單

Agent 的 Windows 程式碼在 Linux 上交叉編譯並通過單元測試，但下列項目**必須在實機上驗證**（建議 Windows 10 22H2 家用版、Windows 11 專業版、一台筆電、一台桌機各一）：

| # | 項目 | 驗證方式 |
|---|---|---|
| 1 | 服務安裝、開機自動啟動、失敗自動重啟 | `install` 後重開機；以工作管理員結束處理程序 |
| 2 | 使用者端助手在登入、切換使用者、登出、鎖定／解鎖時正確啟動與結束 | 快速切換使用者、RDP 登入 |
| 3 | 硬體盤點：WMI 型別轉換、BitLocker、TPM、顯示卡、記憶體插槽 | `nexmis-agent collect` 與系統資訊比對 |
| 4 | 軟體盤點：32／64 位元、每使用者安裝、Microsoft Store 應用程式 | 與「應用程式與功能」比對 |
| 5 | 已安裝更新（QFE）日期在中文地區設定下的解析 | 與 `Get-HotFix` 比對 |
| 6 | 開機時間在「快速啟動」開啟時是否正確 | 關機再開機後比對 |
| 7 | 電池：設計容量、充電容量、循環次數；UPS 是否被誤判為電池 | 與 `powercfg /batteryreport` 比對 |
| 8 | Defender 被動模式／第三方防毒時的判斷 | 安裝第三方防毒的電腦 |
| 9 | 事件記錄錯誤彙整（EvtRender）與服務異常的雜訊程度 | 檢視健康頁問題清單 |
| 10 | CPU／記憶體使用率（NtQuerySystemInformation） | 與工作管理員比對 |
| 11 | 遠端畫面：多螢幕、高 DPI 縮放、旋轉螢幕、滑鼠游標、鍵盤（中文輸入法、Ctrl+Alt+Del） | 實際遠端操作 |
| 12 | 同意視窗、連線提示列、即時訊息視窗、重新開機倒數與延後 | 實際操作 |
| 13 | 以使用者身分執行指令碼（runAsUser） | `whoami` 指令 |
| 14 | 部署 MSI／EXE／MSIX，含 3010 需重開機 | 部署 7-Zip MSI 等常見套件 |
| 15 | Agent 自動更新（替換執行中的執行檔並重啟服務） | 發佈新版本 manifest |
| 16 | 斷網、伺服器重啟、睡眠／休眠恢復後自動重連與離線結果補送 | 拔網路線、休眠 |
| 17 | 資源用量（閒置記憶體、CPU） | 觀察 24 小時 |

驗證結果與發現請記錄為 GitHub Issue，標上 `windows-validation`。

## 10. 開發者交接

- 程式結構、協定與安全設計：見 [architecture.md](architecture.md)；API：見 [api.md](api.md)。
- 新增指令類型：`internal/protocol`（常數與參數結構）→ `internal/server/core/commands.go`（驗證）→ `internal/agent/agent/exec.go`（執行）→ Web 指令表單。
- 新增盤點／健康項目：`internal/agent/collect`（收集與 `EvaluateHealth` 規則）→ `protocol` 結構 → Web 顯示。舊版伺服器會忽略不認得的欄位，可漸進升級。
- 資料庫結構變更：在 `internal/server/store/migrations/` 新增 `000N_xxx.sql`，不要修改已發佈的檔案。
- CI：`.github/workflows/ci.yml`（gofmt、vet（含 Windows）、測試（含 PostgreSQL 整合測試）、交叉編譯、Windows 單元測試、前端 lint 與建置）。
- 發佈：`VERSION=x.y.z scripts/build.sh`，或建置 Docker 映像檔。
