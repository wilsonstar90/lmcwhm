# lmcwhm

自研單機 Linux 伺服器控制面板，定位接近 aaPanel／寶塔：**Go 單一 Binary + Vue 3 / Naive UI**，以網頁管理網站、資料庫、Docker、安全、備份及系統設定。

目前文件對應 **`v0.21.0`**。實際架構、驗證範圍及後續路線見 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)；開發及 AI agent 工作守則見 [AGENTS.md](AGENTS.md)。

## 功能總覽

| 範圍 | 主要功能 |
|---|---|
| 面板核心 | 管理員及子用戶、模組／網站權限、bcrypt、TOTP 2FA、Session + CSRF、安全入口、授權 IP、綁定域名、BasicAuth、面板 SSL、操作日誌 |
| 網站 | PHP、靜態、Node.js、Python、Java、反向代理、TLS 直通、別名、通配符、到期停用、偽靜態、gRPC、自訂錯誤頁 |
| Edge / SSL | HAProxy 80/443、IPv4/IPv6、SNI exact/wildcard routing、Nginx、HTTP-01 / DNS-01、Let's Encrypt、LiteSSL、自訂 ACME CA、自動續期 |
| App Store | 多版本 PHP、Node.js、Python、Java、MySQL、MariaDB、PostgreSQL、MongoDB、SQL Server、Redis、Memcached、Adminer、phpMyAdmin；容器或 source compile 安裝、patch update |
| 資料庫 | 建庫及用戶、權限、設定、Redis keys、phpMyAdmin、備份／匯入、回收站、資料表 repair／optimize／engine／comment／structure tools |
| Docker | Containers、Images、Volumes、Networks、Compose、registries、即時用量、日誌、exec terminal、綁定域名 |
| 檔案 / Terminal | `os.Root` 路徑沙盒、分塊上傳、編輯歷史、權限／擁有者、回收站、分享、全文搜尋、遠程下載、zip/tar.gz/7z/rar、CodeMirror、xterm.js PTY |
| 安全 | nftables、Cloudflare／Linode 封鎖同步、IP/UA/URL lists、可信 IP feeds、WAF + OWASP CRS、CrowdSec、自動封鎖、流量總覽、安全掃描、Webshell 掃描、防篡改 |
| 運維 | Dashboard、資源監控、日誌中心、Cron、systemd 進程守護、撥測、網站報表、告警、FTP/SFTP、WordPress、Composer、Varnish、工具箱 |
| 備份 / 遷移 | 網站、資料庫、Docker volumes、任意目錄及面板備份；本地、Google Drive、OneDrive；整機快照、升級、回滾、離線安裝 |
| 整合 | Cloudflare DNS Zone Editor、郵件伺服器、REST API + OpenAPI、API Token、MCP、Claude／OpenRouter AI 助手 |
| UX | 五種語言、響應式介面、PWA、深色主題、可自訂選單及品牌 Logo、任務訊息盒 |

呢個係**單機管理面板**：子用戶只係操作員，唔係 cPanel tenant；目前冇 Reseller、套餐或多機 Agent。

## 系統需求

- 建議 **Ubuntu 22.04 / 24.04**，root、systemd、Docker、nftables。
- 安裝腳本仍接受 Ubuntu 20.04，但其標準支援已於 2025-05 完結，只應喺 ESM 環境使用。
- 網站功能需要本機 **80 / 443**；面板使用獨立 port。FTP、郵件或資料庫遠端存取會按設定使用額外 ports。
- 本機開發支援 macOS；防火牆、systemd、容器及完整網站 E2E 必須喺隔離 Ubuntu 環境執行，**唔好喺生產 Server 跑測試腳本**。

## 本機開發

需要 Go `1.27.3`、Node.js 22 及 npm。

```bash
make web        # npm ci、TypeScript 檢查及建置 web/dist
make dev-init   # 全新 ./.dev 初始化：admin / admin12345、port 18888、入口 /console
make dev        # https://127.0.0.1:18888/console
make test       # go vet ./... + go test ./...
make linux      # Linux amd64 / arm64 binaries
```

前端熱更新：保持 `make dev`，另開：

```bash
cd web
npm run dev
# http://localhost:5173/console
```

`dev-init` 只應用於全新開發資料目錄；現有 `.dev` 內嘅帳號及設定唔應當作生產備份。

## 安裝到 VPS

以 root 執行：

```bash
curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash
```

自訂 port、安全入口及帳號：

```bash
curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash -s -- --port 8888 --entry /console --username admin
```

其他選項包括 `--password`、`--version v0.21.0`、`--skip-docker`、`--binary ./lmcwhm-linux-amd64`、`--channel beta` 及 `--offline <包>`。

安裝後請立即：

1. 保存隨機產生嘅密碼及安全入口，並修改預設／開發密碼。
2. 喺雲防火牆只放行必要來源及 ports。
3. 啟用面板 SSL、TOTP 2FA，按需要設定授權 IP。
4. 設定 ACME email；需要自動 DNS／DNS-01 時再設定 Cloudflare Token。
5. 另找獨立機器驗證備份還原，唔好只確認備份檔存在。

## 更新、修復及救援

重跑安裝命令會進入修復／更新模式。流程包括備份 `data/`、更換 binary、migration、重啟及健康檢查；啟動失敗會還原舊 binary 同資料。官方 Release 下載必須通過 SHA256 校驗。

| 命令 | 作用 |
|---|---|
| `sudo lmcwhm update` | 更新；可用 `--version` 或 `--channel beta` |
| `sudo lmcwhm rollback` | 回滾 binary；`--with-data` 同時還原更新前資料 |
| `sudo lmcwhm repair` | 重裝目前版本及修復服務 |
| `sudo lmcwhm reset-panel` | 重設安全入口、管理員密碼及面板存取限制 |
| `sudo lmcwhm firewall-off` | 鎖死自己時關閉面板管理嘅 nftables 規則 |
| `sudo lmcwhm snapshot restore <檔案或連結>` | 還原整機快照 |

以上命令會改變實際 Server；執行前必須確認目標機器、版本及備份。

## CLI

直接輸入 `lmcwhm` 顯示選單。常用子命令：

- `default`、`start|stop|restart|reload|status`、`log`
- `passwd`、`username`、`port`、`entry`、`ssl-off`、`2fa-off`
- `domain-unbind`、`ip-clear`、`basicauth-off`、`unlock`
- `update`、`repair`、`reset-panel`、`firewall-off`
- `backup list`、`backup panel`、`backup restore-panel <檔案>`
- `snapshot restore <檔案或連結>`、`mcp --stdio`

CLI 可直接讀寫 `/opt/lmcwhm/data/panel.db`，所以救援命令具有完整管理權限，必須限制 root 登入及 sudo 權限。

## 測試及 CI

本機基本驗證：

```bash
make test
cd web && npm run build
for f in deploy/*.sh; do bash -n "$f"; done
```

GitHub Actions：

- `ci.yml`：Go vet/test、前端及 Linux build、Ubuntu 22.04/24.04 installer E2E、Ubuntu 24.04 sites/apps Docker E2E。
- `source-check.yml`：按修改及每週檢查 source compile recipes。
- `release.yml`：推送 `v*` tag 後測試、建置 amd64/arm64、產生 SHA256SUMS 並建立 Release。

雲帳戶、真實郵件收發、AI API、通知送達、PWA 安裝及離線包需要各自嘅外部驗收；CI 通過唔代表呢啲第三方服務已實測。

## 發佈

正式發佈只應由維護者喺確認 CI、版本內容及備份後執行：

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

一般開發、文件修改及本機測試唔需要落 tag、push 或改版本號。
