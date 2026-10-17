# NEXMIS Web 管理端

React 19 + TypeScript + Vite 8 + Ant Design 6（繁體中文）。建置結果輸出至 `web/dist`，由 `web/embed.go`（`web.Dist`）以 `go:embed` 內嵌進 `nexmis-server`。

## 開發

```bash
cd web
npm ci
npm run dev        # http://localhost:5173，/api 與 /healthz（含 WebSocket）代理至 http://localhost:8080
```

後端位址不同時可用環境變數覆寫：`NEXMIS_BACKEND=http://10.0.0.5:8080 npm run dev`。

## 建置與檢查

```bash
npm run lint       # ESLint（flat config，含 react-hooks / react-refresh）
npm run build      # tsc -b 型別檢查 + vite build → dist/
```

`dist/` 內容不納入版控，只保留 `dist/.gitkeep`（建置時會自動補回），讓未建置前端時 Go 仍可編譯。正式發佈流程：先 `npm run build`，再編譯 `nexmis-server`。

## 目錄結構

| 路徑 | 說明 |
|---|---|
| `src/api/` | `types.ts`（對應 docs/api.md 的型別）、`client.ts`（fetch 包裝、CSRF 標頭、401 導向登入、錯誤處理）、`endpoints.ts`（各端點）、`keys.ts`（react-query 快取鍵） |
| `src/components/` | 共用元件：版面、狀態標籤、目標選擇器、指令編輯器、圖表、即時事件 Provider 等 |
| `src/pages/` | 各頁面（路由層 `React.lazy` 分割） |
| `src/hooks/` | 登入者／角色、主題、事件、網址狀態等 hooks |
| `src/utils/` | 格式化、中文標籤、Cron 說明、指令／目標轉換 |

## 慣例

- 所有請求 `credentials: 'same-origin'`，寫入請求帶 `X-NEXMIS-CSRF: 1`；收到 401 會導向 `/login?next=…`；錯誤以 antd message 顯示伺服器回傳的 `error.message`。
- 即時事件連線 `/api/v1/events/ws`，斷線以指數退避（上限 30 秒、±20% 抖動）重連，並依事件類型 invalidate react-query 快取。
- 依角色（admin > operator > helpdesk > viewer）隱藏不可用的選單與操作；伺服器端仍會再次驗證權限。
- 深色／淺色主題記憶於 `localStorage`（`nexmis.theme`）。
- 遠端檢視頁 `/remote/:sessionId` 為獨立全螢幕頁面，實作 docs/api.md 第 4 節的二進位協定。
