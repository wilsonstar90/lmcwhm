# lmcwhm 架構與驗證

本文描述 **`v0.21.0` 實際實作**，唔再將早期構想當成現狀。最後一節先係尚未完成嘅方向。

## 1. 核心決策

- **單機、單面板、root 運維**：唔做 cPanel 多租戶、Reseller、套餐或共享／獨立站點模式。
- **Go 單一 Binary**：REST API、WebSocket、CLI、MCP、背景服務及內嵌 Vue production build 由同一進程提供。
- **HAProxy 作 Edge**：統一處理 80/443、SNI、Host、TLS 終止或直通，再交 Nginx、Varnish 或後端。
- **Docker CLI 管理運行環境**：Web、runtime、資料庫及應用主要以容器執行；面板核心本身由 systemd 管理。
- **SQLite 作面板元數據**：適合單機，WAL、foreign keys、busy timeout、單一 writer connection。
- **UI、REST、CLI、MCP 共用 service 層**：高風險操作需要明確權限及審計。

## 2. 運行架構

```text
Internet / Cloudflare / Linode Cloud Firewall
                    │
             HAProxy :80 / :443
                    │
       ┌────────────┼──────────────────┐
       │            │                  │
  Nginx / WAF    Varnish          TLS passthrough
       │            │                  │
 PHP / static   back to Nginx      後端自己處理 TLS
 Node / Python / Java / proxy
       │
 Docker runtime、DB、mail、FTP、CrowdSec

 lmcwhm（systemd、Go binary）
 ├─ 內嵌 Vue SPA
 ├─ /api/v1、WebSocket、/mcp、CLI
 ├─ SQLite + migrations
 ├─ task / cron / monitor / alerts / stats workers
 └─ Docker、HAProxy、Nginx、nftables、systemd integrations
```

面板自己監聽獨立 port，預設使用安全入口；唔係一定要經 443 進入。

## 3. 實際技術選型

| 範圍 | 實作 |
|---|---|
| Go | Go 1.27.3、`go-chi/chi/v5`、Cobra |
| DB | `modernc.org/sqlite`；`internal/store/migrations` 順序執行，冇使用 sqlc／goose |
| Frontend | Vue 3、TypeScript、Vite、Naive UI、Pinia、Vue Router、vue-i18n |
| Editors / terminal | CodeMirror 6、xterm.js；唔係 Monaco |
| Charts / maps | 自製 SVG／CSS 圖表、D3 geo；唔係 uPlot |
| WS / PTY | `coder/websocket`、`creack/pty` |
| Docker | `docker` CLI 及 JSON output；唔係 Docker Engine Go SDK |
| ACME | `go-acme/lego/v4`、HTTP-01、Cloudflare DNS-01、EAB |
| Cloud APIs | 自行使用 Cloudflare／Linode HTTP API |
| Backup cloud | 自行實作 OAuth device flow 及 Drive／OneDrive resumable upload；冇使用 rclone |
| GeoIP | DB-IP Lite country／ASN，可選 city database；唔係 GeoLite2 + ip2region |
| MCP | 自行實作 JSON-RPC、Streamable HTTP 及 stdio unix-socket bridge |
| i18n | 前端 vue-i18n；後端穩定錯誤碼及 CLI 自製翻譯 |
| File sandbox | Go `os.Root`，並處理 symlink、archive traversal 及 zip-slip |

## 4. 目錄與主要模組

```text
cmd/lmcwhm/             process entrypoint、serve、CLI、maintenance
internal/
  server/               HTTP routes、middleware、OpenAPI、MCP、permissions
  store/                SQLite connection 及 append-only SQL migrations
  auth/                 users、sessions、TOTP、sub-user permissions
  site/  webenv/        sites、routing、HAProxy/Nginx/PHP/Varnish generation
  appstore/ database/   app catalog、source build、DB management/tools
  dockermgr/ dockerx/   Docker resources 及 command helpers
  files/ fileshare/     file operations、archives、sharing
  firewall/ protect/    nftables、cloud sync、lists、edge protection
  security/ secevents/  WAF、CrowdSec、traffic/security events、auto-ban
  backup/               local/cloud backup、DB import、snapshot
  cron/ tasks/          scheduled jobs 及 background task execution
  monitor/ sysinfo/     metrics、host detail、dashboard
  ai/ mcp/              read-only AI tools、MCP protocol
  ...                   acme、mail、ftp、wordpress、geoip、audit 等
web/src/                Vue views、components、stores、locales
deploy/                 install、offline bundle、Linux E2E scripts
.github/workflows/      CI、source-check、release
```

## 5. 資料與檔案位置

預設 panel home 係 `/opt/lmcwhm`，開發可用 `LMCWHM_HOME` 隔離。

- `data/panel.db`：面板設定、用戶、網站、任務、安全及應用元數據。
- `data/master.key`：AES-256-GCM 加密本機保存嘅第三方 credentials；權限 `0600`。
- `data/ssl/`：面板證書。
- `apps/`、`web/`、`backup/`、`logs/`、`geoip/`：各類運行資料。
- 網站預設 `/www/wwwroot/<domain>`，日誌 `/www/wwwlogs/`；新版可由面板設定調整部分根目錄。
- 檔案管理預設 root `/`，可用 `LMCWHM_FILES_ROOT` 收窄。

加密 key 同資料庫放喺同一台機只係保護靜態外洩，唔可以抵抗 root compromise；備份及還原必須同時保護兩者。

## 6. Edge、網站與應用

### Routing

- 每個 hostname 只有一個 site owner；SNI 頁係 `sites` 資料嘅 read model，冇另一份可漂移路由表。
- exact match 優先於 wildcard；較深 wildcard 優先於較淺。
- TLS terminate 由 Edge 使用域名證書；passthrough 將 ClientHello 原封轉交後端。
- HAProxy 設定由 DB 重新生成，先 `haproxy -c`，再 master-worker reload，失敗還原；**冇使用 Runtime API 逐條改 map**。
- Nginx vhost 同樣先驗證再套用；真實 IP、CDN trusted ranges、黑白名單及 protection exemptions 會生成入 Edge 設定。
- gRPC 網站需要繞過會阻礙 streaming body 嘅 ModSecurity 處理，唔等於全局關閉 WAF。

### Runtime 與 App Store

- PHP 每版本獨立容器、設定、extensions、FPM pool、狀態及 slow log。
- Node、Python、Java 項目以獨立容器及 restart policy 運行；一般自訂長駐命令則可經 systemd process guardian。
- 資料庫預設只發佈到 loopback；遠端存取要同步防火牆來源限制。
- App Store catalog 內置於 binary，支援容器 image 或 source compile；source recipes 有獨立 GitHub workflow。
- App／DB 設定改動先驗證；更新及還原操作經背景任務記錄。

## 7. 認證、授權與高風險操作

- Browser：HttpOnly + SameSite session、非 GET CSRF、bcrypt、TOTP、按 IP 登入鎖定。
- Gates：授權 IP → 綁定域名 → BasicAuth → 安全入口；可自訂未認證回應。
- API Token：只保存 SHA-256 hash，scope 為 read／operate／admin，可設 expiry 及 IP allowlist。
- 子用戶：按模組及可選網站 IDs 授權；未映射嘅新 API route 預設只限管理員。
- 全局 running tasks、task history 及清除歷史只限管理員。隨機 12 字元 task ID 作為 caller 查詢自己背景工作進度嘅 capability，避免列出或讀取他人任務。
- Terminal 開啟前需要再次驗證；主機及容器 terminal 都係高權限功能。
- MCP AI 助手只提供 read-only tools；operate／admin MCP tools 具有真實系統修改能力，全部需要審計。

## 8. 背景服務與一致性

- `tasks.Manager` 序列化重型工作，並為備份等長工作提供較長 timeout。
- Task logs 短期保存在 memory，完成後寫入 SQLite history，設數量及長度上限。
- Cron 支援秒級 schedule、timeout、避免重疊、輸出及告警；SSL renew、security scan、app update check 等係內建 jobs。
- Monitor、probe、site stats、security events、CrowdSec、GeoIP refresh、mail certificate sync 各自以 goroutine 運行。
- SQLite 只允許一個 writer；migration 逐個 transaction 執行，新 migration 只應追加，唔應修改已發佈版本。

## 9. 備份、快照與更新

- 網站及目錄使用 safe archive 處理；資料庫使用引擎工具 dump／restore。
- Panel backup 以 SQLite `VACUUM INTO` 建立一致快照。
- Drive／OneDrive 採用 device OAuth、分塊及續傳；token 加密保存。
- Snapshot 包含面板 home、App 數據及網站；複製 App 數據時短暫 pause 容器，還原後重建 web stack。
- Installer 先下載新 binary，官方 Release 必須取得並通過 SHA256SUMS，再備份資料、停止服務、替換、migration 及健康檢查。
- Rollback 預設還原 binary；`--with-data` 會同時還原舊資料，屬高風險操作。

## 10. 測試與交付

### 本機

```bash
go vet ./...
go test ./...
cd web && npm run build
for f in deploy/*.sh; do bash -n "$f"; done
```

### GitHub Actions

- 每次 push／PR：Go、前端、Linux build。
- Ubuntu 22.04／24.04：fresh install、upgrade、reset-panel、rollback。
- Ubuntu 24.04：真 Docker websites、SNI、PHP、ACME/Pebble、apps、DB、files、firewall、WAF、CrowdSec 等 E2E。
- Source-check：按相關修改及每週跑官方 images／source configure matrix。
- Tag release：測試、跨平台 build、checksums、GitHub Release。

E2E scripts 會安裝套件、操作 Docker、nftables 及 systemd，只可以在拋棄式 Ubuntu runner／VM 執行，**唔可以在使用中的 Server 執行**。

### 第三方驗收邊界

單元測試及 CI 唔代表以下項目已完成真實帳戶驗收：

- Google Drive／OneDrive、真實 Cloudflare／Linode 生產帳戶。
- Claude／OpenRouter API、Email／Telegram／Slack 等實際送達。
- docker-mailserver 實際收發、ClamAV、離線安裝包、PWA 安裝及 beta channel。
- 前端目前主要依靠 TypeScript build 及 E2E，冇獨立 Vitest／Playwright suite。

## 11. 尚未完成／後續方向

- 多 Web Server（OpenResty、Apache、OpenLiteSpeed）。
- 多機 Agent、集中節點管理及寶塔／aaPanel 遷移工具。
- WordPress clone／staging、國家訪問限制等早期藍圖功能。
- 更完整嘅前端自動化測試、真實第三方服務驗收及災難還原演練。
- 任務模型可進一步保存 owner／module，取代單靠隨機 task ID capability 嘅查詢方式。

早期設計中嘅 Runtime API、rclone、Monaco、uPlot、sqlc／goose、官方 MCP SDK、Git/HTTP App Store repository、插件 gRPC 及事件總線，唔係目前實作，不應再當成已完成架構。
