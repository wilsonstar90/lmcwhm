# lmcwhm 架構與驗證

本文描述 **`v0.24.0` 實際實作**，唔再將早期構想當成現狀。最後一節先係尚未完成嘅方向。

## 1. 核心決策

- **單機、單面板、root 運維**：唔做 cPanel 多租戶、Reseller、套餐或共享／獨立站點模式。
- **Go 單一 Binary**：REST API、WebSocket、CLI、MCP、背景服務及內嵌 Vue production build 由同一進程提供。
- **HAProxy 作 Edge**：統一處理 80/443、SNI、Host、TLS 終止或直通，再交 Nginx、Varnish 或後端。
- **Docker CLI 管理運行環境**：Web、runtime、資料庫及應用主要以容器執行；面板核心本身由 systemd 管理。
- **SQLite 作面板元數據**：適合單機，WAL、foreign keys、busy timeout、單一 writer connection。
- **UI、REST、CLI、MCP 共用 service 及 Action 層**：高風險操作需要明確權限及審計；快速寫入維持同步，共享配置及重型工作使用持久任务。

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
 ├─ /api/v1、WebSocket、/mcp/{uuid}、CLI
 ├─ SQLite + migrations
 ├─ Action / durable tasks / cron / monitor / alerts / stats workers
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
- `recovery/state.json`：watchdog 狀態。Backup recovery key 預設喺 panel home 嘅上一級目錄，唔會打入 panel backup；用戶仍需離線保存。
- 網站預設 `/www/wwwroot/<domain>`，日誌 `/www/wwwlogs/`；新版可由面板設定調整部分根目錄。
- 檔案管理預設 root `/`，可用 `LMCWHM_FILES_ROOT` 收窄。

加密 key 同資料庫放喺同一台機只係保護靜態外洩，唔可以抵抗 root compromise；備份及還原必須同時保護兩者。

## 6. Edge、網站與應用

### Routing

- 每個 hostname 只有一個 site owner；SNI 頁係 `sites` 資料嘅 read model，冇另一份可漂移路由表。
- exact match 優先於 wildcard；較深 wildcard 優先於較淺。
- TLS terminate 由 Edge 使用域名證書；passthrough 將 ClientHello 原封轉交後端。
- HAProxy 同 Nginx 都先寫好並驗證，再決定實際需要更新嘅部分，避免一邊驗證失敗而另一邊已提前套用。純配置操作冇變化唔 reload；現有 map／ACL 優先經 Runtime API 更新，其他改動或 runtime 更新失敗先 master-worker hot reload。`ensureStack` 可能已先寫新配置，所以建站、WAF、更新及修復流程保留強制 reload，唔會因 snapshot 相同而漏套用。
- Nginx 配置有實際變更時改用快速容器重啟並等待 ready，避免 graceful reload 嘅舊 worker 長時間提供舊規則及累積記憶體；冇變更唔重啟，HAProxy 則保留 hot reload。
- Nginx vhost 同樣先驗證再套用；真實 IP、CDN trusted ranges、黑白名單及 protection exemptions 會生成入 Edge 設定。
- Client IP 以即時 TCP peer 判斷信任：Cloudflare 公網 ranges、自訂 CDN ranges，或明確啟用 Cloudflare Tunnel 後嘅 loopback cloudflared。每網站 CDN Header 唔再接受任意來源；面板、REST、MCP、登入鎖定及審計共用同一 Cloudflare／Tunnel resolver，並保留 Peer IP 同 Client IP 嘅分別。
- gRPC 網站需要繞過會阻礙 streaming body 嘅 ModSecurity 處理，唔等於全局關閉 WAF。

### Runtime 與 App Store

- PHP 每版本一個容器，內含共享 Low（ondemand）及 Busy（dynamic）pools。現有網站維持 Busy；新網站預設 Low＋Auto。Auto 每 10 分鐘按 24 小時流量、FPM queue 同 max-children 指標，以持續門檻及 6 小時冷卻判斷，唔會逐請求切換。兩個 pools 已 ready，切換只改 upstream，經 `nginx -t` 同 HUP graceful drain，失敗會還原設定。容量建議按全部已安裝版本共用同一 RAM budget。
- WP-CLI 可作 App Store 工具安裝，掛載網站目錄並以常駐容器提供命令；WordPress Toolkit 優先使用，未安裝時先用內置 phar。WP-CLI 下載或個別插件／主題列表失敗只顯示警告，唔再令整個網站設定頁 500。
- Node、Python、Java 項目以獨立容器及 restart policy 運行。Deploy 先複製到新 release、安裝 dependencies，再原子切換 `.lmcwhm/current`；保留三份 releases，可回滾上一版，restart 本身唔再重裝 packages。一般自訂長駐命令則可經 systemd process guardian。
- 資料庫預設只發佈到 loopback；遠端存取要同步防火牆來源限制。
- App Store catalog 內置於 binary，支援容器 image 或 source compile；source recipes 有獨立 GitHub workflow。
- App／DB 設定改動先驗證；更新及還原操作經背景任務記錄。

## 7. 認證、授權與高風險操作

- Browser：HttpOnly + SameSite=Strict session、非 GET CSRF、bcrypt、TOTP、按 IP 登入鎖定。
- PWA：iOS/iPadOS 16.4+ 加入主畫面後可用 Web Push。App Push 係 alert channel，裝置訂閱獨立於 login session，logout 唔會撤銷。VAPID private key、endpoint 同裝置 keys 加密保存，可逐部裝置撤銷及測試。VAPID subject 使用 ACME email 或 HTTPS 面板網域，避免 `.local` contact 被 push service 拒絕。
- Gates：授權 IP → 綁定域名 → BasicAuth → 安全入口；可自訂未認證回應。安全入口 cookie 使用 HttpOnly + SameSite=Lax，讓 Cloudflare Access 等外部身份提供者完成頂層 GET 跳轉後仍能進入登入頁；呢個 cookie 只證明已通過入口，唔係登入 session。
- API Token：驗證使用 SHA-256 hash；新 Token 另用面板 master key 以 AES-256-GCM 加密保存，管理員可按需翻查。scope 為 read／operate／admin，可設 expiry、IP allowlist、編輯到期日或即時暫停／恢復。每個 Token 有獨立 UUIDv4 MCP endpoint，URL 與 Token 必須匹配；可限制只用 Kimi／DeepSeek／Claude／Codex，身份來自 MCP clientInfo 及 User-Agent。舊 hash-only Token 不可翻查，只可撤銷後重建。
- 子用戶：按模組及可選網站 IDs 授權；未映射嘅新 API route 預設只限管理員。
- 全局 running tasks、task history 及清除歷史只限管理員。隨機 12 字元 task ID 作為 caller 查詢自己背景工作進度嘅 capability，避免列出或讀取他人任務。
- Terminal 開啟前需要再次驗證；主機及容器 terminal 都係高權限功能。
- 可選 `LMCWHM_PRIVILEGED_HELPER=1` 會啟動獨立 root helper subprocess，以 `0600` Unix socket 接受固定 Docker／proxy actions；呢個係 process boundary，唔代表面板已非 root 化，亦唔接受任意 shell。
- MCP AI 助手只提供 read-only tools；operate／admin MCP tools 具有真實系統修改能力，全部需要審計。Tool arguments 會按欄位 redact secrets；`run_shell` 只記錄 command SHA-256，唔記錄原文，shell output 喺串流寫入時已限制為最後 100 KiB。
- `list_api_endpoints` / `call_api` 經主 router 覆蓋所有已註冊 JSON REST 路由，按 method、模組及 Token scope 授權。通用調用以乾淨 routing context 執行真實 handler，只略過已驗證 MCP caller 嘅瀏覽器 gate；審計只記錄 method 及移除 query 後嘅 path，唔記錄 body。

## 8. 背景服務與一致性

- 所有非 GET REST 請求先經 `actionMiddleware`，專用 MCP mutation 及 cron 亦使用同一 `tasks.Action` metadata；快速操作保持同步且不改變回應。現有 `Start`、`StartLong`、`RunSync` 工作統一經 Action 執行，共享 resource 有獨立鎖，重型工作保留全局序列化或較長 timeout。
- Task 支援 queued／running／success／failed／cancelled，排隊時間亦計入 timeout；可取消，log 定期 checkpoint，owner/module 一併保存。面板重啟時，queued／running 工作標記 interrupted／failed，唔會自動重跑非冪等操作。`PersistResult` 操作可用 idempotency key 讀回已完成結果；共享 web apply 繼續使用 coalescing。
- 同一 idempotency key 喺 queued／running 時唔會重複開 task；resource 同 exclusive locks 支援 context cancellation。
- 網站防護、CDN 設定、專屬規則及黑白名單維持「回應時已套用」嘅同步 API 合約，底層共用 task 完成後先回應，並只額外加入 `task_id`；對應 Vue 元件共用 TaskModal 並防止重複提交。Vue 仍嵌入單一 binary，呢個版本冇強行改變部署或特權邊界。
- Cron 支援秒級 schedule、timeout、避免重疊、輸出及告警；SSL renew、security scan、app update check 等係內建 jobs。
- Monitor、probe、site stats、security events、CrowdSec、GeoIP refresh、mail certificate sync 各自以 goroutine 運行。`/healthz` 唔經瀏覽器 gates，只回報 DB 與 apps／web／cron 嘅 ready／not_ready；啟動未完成或失敗回 503，唔暴露內部錯誤內容。
- Watchdog 覆蓋 MariaDB、PHP、Varnish、Redis、PostgreSQL、MongoDB、HAProxy 同 Nginx；readiness 包括引擎指令或 published-port 連線。Failure、cooldown、attempts 同 recovering 狀態持久化，重啟唔會重設限制；獨家工作及 Docker resource lock 進行時唔介入，達上限只告警，唔 reboot 全機。
- SQLite 保持單 writer；另有最多 4 個 connections 嘅 WAL query-only read pool，先用於高讀取查詢。Migration 逐個 transaction 執行，新 migration 只應追加，唔應修改已發佈版本。

## 9. 備份、快照與更新

- 網站及目錄使用 safe archive 處理；資料庫使用引擎工具 dump／restore。
- Panel backup 以 SQLite `VACUUM INTO` 建立一致快照。
- 新 archive 喺寫入時同步計算 SHA256，並附獨立 `.manifest.json` 記錄格式、panel 版本、目標、大小、建立時間及 checksum。本地 copy 會再驗 hash；雲端 archive 會 download readback 驗證後先上傳 manifest。還原前會先讀到臨時檔並驗證，失敗唔會開始改動目標；舊備份冇 checksum 時保持兼容。
- 可選備份加密使用分段 AES-256-GCM、每份隨機 nonce prefix 同獨立 recovery key；key 檔位於 panel home 以外，唔會打入 panel backup。Manifest 同時保存明文及密文 checksum。停用只影響新備份，舊 key 保留作還原。
- 還原演練會喺臨時目錄解包網站／path／volume，檢查 panel／snapshot SQLite integrity，或喺臨時資料庫真正 import 後刪除。每次演練保存結果；`LMCWHM_BACKUP_WORKER=1` 可先用同一 binary 嘅固定 worker subprocess 驗證本地 archive。
- Drive／OneDrive 採用 device OAuth、分塊及續傳；token 加密保存。
- Snapshot 包含面板 home、App 數據及網站；複製 App 數據時短暫 pause 容器，還原後重建 web stack。
- Installer 以 `flock` 防止並發更新，先下載新 binary，官方 Release 必須取得並通過 SHA256SUMS，再備份資料、停止服務、替換、migration 及 `/healthz` 健康檢查。Docker 不健康時預設 fail-closed，只有 `--repair-docker` 才允許 restart／reinstall。
- Rollback 預設還原 binary；`--with-data` 會同時還原舊資料，屬高風險操作。Installer 自動 rollback 後會再驗證舊版本健康狀態。

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
- Ubuntu 24.04：真 Docker websites、SNI、PHP、ACME/Pebble、apps、DB、files、firewall、WAF、CrowdSec 等 E2E。Apps runner 使用公開 `mirror.gcr.io` registry mirror，減少 Docker Hub unauthenticated rate limit；image tags 及測試範圍不變。
- Source-check：按相關修改及每週跑官方 images／source configure matrix。
- Tag release：測試、跨平台 build、checksums、GitHub OIDC cosign keyless 以 new bundle format 簽署 `SHA256SUMS`，發佈 binary、checksums、Sigstore bundle 及 installer。

E2E scripts 會安裝套件、操作 Docker、nftables 及 systemd，只可以在拋棄式 Ubuntu runner／VM 執行，**唔可以在使用中的 Server 執行**。

### 第三方驗收邊界

單元測試及 CI 唔代表以下項目已完成真實帳戶驗收：

- Google Drive／OneDrive、真實 Cloudflare／Linode 生產帳戶。
- Claude／OpenRouter API、Email／Telegram／Slack 等實際送達。
- docker-mailserver 實際收發、ClamAV、離線安裝包及 beta channel。PWA 已做 iPhone／iPad viewport 同 mock Web Push 測試，但真實 iOS 裝置安裝及 Apple Push 送達仍需實機驗收。
- 前端目前主要依靠 TypeScript build 及 E2E，冇獨立 Vitest／Playwright suite。

## 11. 尚未完成／後續方向

- 多 Web Server（OpenResty、Apache、OpenLiteSpeed）。
- 多機 Agent、集中節點管理及寶塔／aaPanel 遷移工具。
- WordPress clone／staging、國家訪問限制等早期藍圖功能。
- 更完整嘅前端自動化測試、真實第三方服務驗收及災難還原演練。
- 任務模型可進一步保存 owner／module，取代單靠隨機 task ID capability 嘅查詢方式。

早期設計中嘅完整 Runtime API 路由管理、rclone、Monaco、uPlot、sqlc／goose、官方 MCP SDK、Git/HTTP App Store repository、插件 gRPC 及事件總線，唔係目前實作，不應再當成已完成架構。
