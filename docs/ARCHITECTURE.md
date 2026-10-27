# 自研伺服器控制面板（代號 lmcwhm）— 架構與功能藍圖 v2

## Context
由零開始（`/Users/lmc/Claude/whm` 目前係空目錄）開發一套 **aaPanel/寶塔式單機管理面板**，核心用 **HAProxy SNI（443）+ Host（80）邊緣分流**，整合 Cloudflare、Linode Cloud Firewall、雙重 WAF、CrowdSec、Docker、MCP AI 運維同多語言。
已確認：**後台 Go 單一 Binary**、**前端 Vue 3 + Naive UI**、**唔做 cPanel 多租戶 / Reseller / 套餐**，亦**唔分獨立/共享模式** — 統一網站模型；邏輯上分 **網站管理** 同 **Docker 管理** 兩大模組。系統設定、CLI 工具全面跟 aaPanel 嘅做法。

> ⚠️ Ubuntu 20.04 標準支援已於 2025-05 結束（只剩 ESM）。以 20.04 為主要目標，安裝腳本同時支援 22.04 / 24.04。HAProxy 用官方 Docker image 3.0 LTS（`network_mode: host`），唔用 20.04 內建嘅 2.0。

---

## 1. 參考整合（取 aaPanel / 寶塔 優點）

| 參考 | 採納 | 落地 |
|---|---|---|
| aaPanel/寶塔 系統設定 | 面板端口、安全入口、面板 SSL、2FA、授權 IP、綁定域名、BasicAuth、Session 超時 | §3.1 面板設定 |
| `bt` CLI | 數字選單 + 子命令救援工具 | §3.2 `lmcwhm` |
| 網站項目類型 | PHP / Java / Node / Python / HTML | §3.4 Runtime 插件化 |
| 軟件商店 | 一鍵多版本安裝 | §3.5 App Store |
| 寶塔 IP 歸屬地 | 日誌/登入/攻擊 IP 顯示地區 | §3.12 |
| cPanel | 只取「一切皆 API」+ 完整備份/還原思路，**唔要多租戶** | REST API 同時俾 UI / CLI / MCP 用 |

---

## 2. 總體架構

```
            Internet (IPv4 + IPv6)
                    │
   ┌────────────────┴─────────────────┐
   │ Linode Cloud Firewall (API 管理)   │ ← 網絡層白名單/封鎖（最外層）
   └────────────────┬─────────────────┘
   ┌────────────────┴─────────────────┐
   │ Cloudflare（可選橙雲）              │ ← DNS A/AAAA、WAF、Firewall Rules
   └────────────────┬─────────────────┘
          :443 (SNI) │ :80 (Host)
┌───────────────────▼──────────────────────────────────────────┐
│ Edge HAProxy 3.0 LTS  bind :::443 v4v6 / :::80 v4v6           │
│  fe_tls  (tcp) req.ssl_sni → map_dom(sni.map)                 │
│     ├─ mode=passthrough → 後端容器自己持證書 (send-proxy-v2)    │
│     └─ mode=terminate   → fe_https (crt-list) → be_web         │
│  fe_http (http) Host → map → 301 HTTPS / 純 HTTP 站 / ACME     │
│  Runtime API socket ← lmcwhm 動態 add/del map、熱換證書（免 reload）│
└───────┬──────────────────────────────────┬───────────────────┘
        │                                  │
┌───────▼──────── 網站管理 ──────────┐ ┌──────▼──── Docker 管理 ─────┐
│ web: Nginx + ModSecurity + CRS     │ │ 任意容器 / Compose 項目       │
│ runtimes (App Store 安裝):          │ │ Images / Volumes / Networks  │
│  php74…php83-fpm · java(jdk) ·     │ │ 可綁域名 → Edge 路由          │
│  node · python · html(static)      │ └──────────────────────────────┘
│ db (App Store): MySQL/MariaDB ·    │
│  PostgreSQL · MongoDB · SQL Server │
│  · Redis · SQLite(檔案)            │
│ cache: Redis/Memcached/Varnish     │
└────────────────────────────────────┘
┌──────────────────────────────────────────────────────────────┐
│ lmcwhm (Go Binary, systemd) — 內嵌 Vue 前端 (go:embed)          │
│  面板自己監聽 <panel_port>（預設隨機 8000-60000）+ 安全入口 /console │
│  REST /api/v1 · WS /ws · MCP /mcp · CLI 子命令                  │
│  元數據：SQLite（WAL，單機足夠、零依賴，似 aaPanel）              │
│  背景：任務佇列（內建 goroutine worker + SQLite 持久化）           │
└──────────────────────────────────────────────────────────────┘
```

### 2.1 Edge 分流要點
- **443**：只睇 ClientHello SNI；每個域名一條 map 記錄 `domain → backend,mode`。網站管理嘅站點預設 terminate（統一由 HAProxy 持證書、方便 WAF/日誌）；Docker 項目可揀 passthrough（例如自帶 TLS 嘅應用）。
- **80**：HTTP Host 路由；每站可設「強制 HTTPS 301」或保留 HTTP；`/.well-known/acme-challenge/` 轉面板做 HTTP-01。
- 真實 IP：passthrough 用 PROXY v2；terminate 加 `X-Forwarded-For`；橙雲時信任 CF IP 段取 `CF-Connecting-IP`。
- 無 SNI / 未知域名 → `be_default`（空白頁或 drop，防掃描）。
- 雙棧：Edge 監聽 v4+v6，容器內部只需 IPv4 bridge。

### 2.2 Go 技術選型
| 用途 | 套件 |
|---|---|
| HTTP | `go-chi/chi` v5 |
| DB | SQLite `modernc.org/sqlite`（純 Go 免 CGO）+ `sqlc` + `goose` |
| Docker | `github.com/docker/docker/client` |
| 監察 | `shirou/gopsutil/v4` |
| Cron | `robfig/cron/v3`（秒級） |
| WS / PTY | `coder/websocket` / `creack/pty` |
| CLI | `spf13/cobra` + 互動選單 |
| ACME | `go-acme/lego/v4`（Let's Encrypt + LiteSSL，支援 EAB、HTTP-01 / DNS-01） |
| Cloudflare / Linode | `cloudflare/cloudflare-go` / `linode/linodego` |
| 備份雲端 | `rclone/rclone` backend 庫（Google Drive、OneDrive，OAuth + 分塊上傳） |
| IP 歸屬地 | `oschwald/maxminddb-golang`（GeoLite2-City/ASN）+ `ip2region`（中文地區更準） |
| MCP / i18n | `modelcontextprotocol/go-sdk` / `nicksnyder/go-i18n/v2` |
| 檔案沙盒 | Go 1.24+ `os.Root` |

---

## 3. 功能模組

### 3.1 面板系統設定（跟 aaPanel）
- **面板端口**：可改（檢查佔用、自動同步 Linode Firewall / UFW 放行），改完自動重啟。
- **安全入口**：如 `/console`，設定後只可經該路徑登入，其他路徑回 404（唔洩露面板存在）。
- **面板 SSL**：自簽 / Let's Encrypt / LiteSSL / 上傳自有證書；可開關。
- 面板帳號/密碼、TOTP 2FA、BasicAuth 額外保護、**授權 IP**、**綁定域名**（只可經指定域名進入）、Session 超時、登入失敗鎖定、面板別名、時區 & 時間同步、IPv6 監聽開關、離線模式、API 接口（Token + IP 白名單）、通知渠道（Email / Telegram / Webhook）、語言。
- 面板亦可經 443 用域名進入（SNI map `panel.domain → lmcwhm`），兩種方式並存。

### 3.2 SSH 命令 `lmcwhm`（似 `bt`）
- `lmcwhm` 無參數 → 數字選單；亦支援子命令：
  `start|stop|restart|reload|status`、`default`（顯示面板地址/入口/用戶）、`passwd`、`username`、`port <n>`、`entry <path>|--disable`、`ssl --disable`、`2fa --disable`、`domain --unbind`、`ip --clear`（清授權 IP）、`unlock`（清登入限制）、`log`（面板錯誤日誌）、`repair`、`update`、`uninstall`、`mysql-root-passwd`、`firewall allow <port>`、`backup run`、`mcp --stdio`。
- CLI 直接讀寫本機 SQLite/設定，**面板唔運行時都可救援**。

### 3.3 Docker 管理
- 容器：列表、啟停/重啟/刪除、即時 CPU/RAM、WS 日誌串流、`exec` 終端。
- Images（pull/刪除/清理）、Volumes、Networks、Compose 項目（上傳/編輯 yml，一鍵 up/down）。
- 容器/Compose 服務可「綁定域名」→ 自動寫 Edge 路由 + SSL + DNS。
- 系統容器加 `lmcwhm.managed=true` 標籤防誤刪。

### 3.4 網站管理
- **內建 PHP**；App Store 裝咗 runtime 後先出現 **Java（jar/Spring Boot）、Node（npm/pm2）、Python（uwsgi/gunicorn/command）、HTML 靜態** 項目類型。Runtime 以插件介面實現：`Runtime{Install, CreateProject, Start, Stop, Logs, Health}`。
- 建站流程：域名/別名 → 類型 + 版本 → （可選）建 DB → CF DNS A+AAAA → SSL → Edge 路由；一條任務鏈，失敗回滾，前端 WS 睇進度。
- 偽靜態模板（WordPress、Laravel、ThinkPHP、SPA try_files…）、重定向、反向代理、目錄密碼、防盜鏈、強制 HTTPS / HSTS、流量限制、站點日誌、PHP 版本切換 / 擴展 / php.ini、WordPress 一鍵部署、Git webhook 部署。

### 3.5 App Store（一鍵多版本）
- `catalog/<app>/<version>/manifest.yaml` + `compose.tmpl`（image、參數、健康檢查、依賴、面板入口）。
- Runtime：PHP 7.4–8.3（EOL 標示）、JDK 8/11/17/21、Node 18/20/22、Python 3.8–3.12。
- 資料庫：MySQL 5.7/8.0/8.4、MariaDB 10.6/10.11/11.4、PostgreSQL 13–17、MongoDB 6/7、SQL Server 2019/2022（需 ≥2GB RAM 檢查）、Redis 6/7、SQLite（只係檔案管理 + 線上查詢）。
- 其他：Memcached、Varnish、**phpMyAdmin**（MySQL/MariaDB 必備，面板一鍵登入）/ Adminer / pgAdmin / Mongo Express。

#### 3.5.1 每個已安裝軟件嘅「設定」視窗（跟 aaPanel 軟件設定）
每個版本獨立管理（例如 PHP 7.4 同 PHP 8.3 各自一套），manifest 定義有咩 tab：

| 軟件 | 安裝列表（擴展/插件） | 配置修改 | 其他 tab |
|---|---|---|---|
| **PHP 每版本** | 擴展清單逐個裝/卸：opcache、redis、memcached、imagick、swoole、xdebug、ioncube、sodium、intl、gd、zip、pdo_mysql/pgsql/sqlsrv、mongodb、exif、fileinfo、apcu…（顯示已裝/可裝/該版本唔支援） | `php.ini` 全文編輯（Monaco）+ 常用項表單（memory_limit、upload_max_filesize、post_max_size、max_execution_time、display_errors、date.timezone）、`disable_functions` 勾選、`php-fpm.conf` pool | 服務啟停/重載、性能調整（pm 模式、max_children 按 RAM 建議值）、負載狀態（fpm status）、錯誤日誌、慢日誌、切換 CLI 預設版本 |
| **MySQL / MariaDB 每版本** | 插件/組件（如 validate_password、audit） | `my.cnf` 全文編輯 + 表單（innodb_buffer_pool_size、max_connections、character set、bind-address） | 性能調整預設（1G/2G/4G/8G/16G RAM 方案一鍵套用）、當前狀態（QPS、連接數、緩衝命中率）、慢查詢日誌、錯誤日誌、root 密碼、存儲位置、端口、phpMyAdmin 入口 |
| **Redis 每版本** | 模組（RedisJSON、RediSearch、RedisBloom） | `redis.conf` 全文編輯 + 表單（bind、port、requirepass、maxmemory、maxmemory-policy、timeout、databases） | 持久化（RDB/AOF）設定、負載狀態（INFO：記憶體、命中率、連接數、keys）、日誌 |
| 其他（PG/Mongo/MSSQL/Memcached/Varnish/JDK/Node/Python） | 同一框架：Node 全域 npm 包、Python pip 包、PG extensions | 對應主設定檔 | 狀態 + 日誌 |

- 實作：擴展安裝 = 喺該版本容器 `docker-php-ext-install` / `pecl install` 後 commit 成 `lmcwhm/php:<ver>-<hash>` image（重建可復現），或預建「全擴展 image」+ ini 啟用/停用（速度快，預設用此方式）。
- 配置修改：保存前語法檢查（`php-fpm -t`、`mysqld --validate-config`、`redis-server --test-config`），失敗唔覆蓋並顯示錯誤；每次保存自動留歷史版本可回滾。
- phpMyAdmin：作為 App Store 項目安裝，經面板反代 + 臨時 token 單點登入，唔直接暴露公網端口。

### 3.6 資料庫管理
- 統一介面 `DBDriver{CreateDB, CreateUser, Grant, Dump, Restore, ChangePassword, Query}`，按已安裝引擎動態出現 tab：MySQL、PostgreSQL、MongoDB、SQL Server、Redis（key 瀏覽/清空/持久化設定）、SQLite（上傳/瀏覽/查詢）。
- 遠端存取開關（同步 Linode Firewall 規則）、慢查詢日誌。

### 3.7 SSL
- CA：**Let's Encrypt**、**LiteSSL**（ACME + EAB 憑據）；驗證：HTTP-01（經 port 80）或 DNS-01（Cloudflare，支援 Wildcard）。
- 到期前 30 日自動續期，失敗通知；上傳自有證書；經 HAProxy Runtime API 熱載入。

### 3.8 Cloudflare 整合
- 多個 CF 帳戶 Token（AES-GCM 加密存）；DNS 記錄 CRUD、自動 A/AAAA（自動偵測公網 v4/v6）、橙雲開關、WAF Managed Rules、Security Level / Under Attack、Custom Rules、清快取。

### 3.9 防火牆（三層）
- **Linode Cloud Firewalls API**：設定 Linode Token → 列出/建立 Firewall、綁定本 Linode、管理 inbound/outbound 規則（port、v4/v6 CIDR）；面板改端口 / 開 DB 遠端 / 封 IP 時自動同步。
- **系統防火牆**：nftables（UFW 相容），端口規則、IP 黑白名單、Ping 開關、SSH 端口修改。
- 規則同步策略：封鎖 IP 可揀落 nftables（即時）/ Cloudflare（邊緣）/ Linode（網絡層，受規則數上限限制，只放持久黑名單）。

### 3.10 WAF + CrowdSec
- 第一層 Cloudflare；第二層 Nginx ModSecurity v3 + OWASP CRS v4（每站開關、Paranoia Level、誤殺白名單）。
- CrowdSec agent + LAPI：讀 nginx/ModSec/SSH/面板登入日誌；bouncer 落 nftables + Cloudflare；可選 Community Blocklist；面板顯示攔截記錄連 IP 歸屬地。

### 3.11 日誌管理與分類
統一日誌中心，分類 tab：
| 分類 | 來源 |
|---|---|
| 面板操作 | 審計日誌（誰、幾時、IP、做咗咩） |
| 面板登入 | 成功/失敗、IP + 歸屬地 |
| SSH 登入 | `/var/log/auth.log` 解析，IP + 歸屬地 |
| 網站 | 每站 access / error log |
| Runtime | PHP-FPM slow/error、Java/Node/Python stdout |
| 資料庫 | 各引擎 error / slow log |
| Docker | 容器 stdout/stderr |
| 系統 | journald / syslog / kern |
| 安全 | WAF 攔截、CrowdSec decisions、防火牆 drop |
| 計劃任務 | 每次 Cron 執行輸出 |
| 備份 / SSL | 任務結果 |
- 功能：即時 tail（WS）、關鍵字/時間/IP 篩選、下載、清理、保留策略、logrotate 生成；異常偵測 Worker（5xx 突增、404 掃描、高頻 IP、SQLi pattern）→ 警報 + 交 CrowdSec；MCP 俾 AI 解讀。

### 3.12 IP 歸屬地
- 本地 mmdb 查詢（GeoLite2 + ip2region），每月自動更新；顯示國家/地區/城市/ISP/ASN + 國旗。
- 用喺：日誌中心、登入記錄、網站訪問統計、威脅 IP、防火牆規則。

### 3.13 備份
- 對象：網站檔案、資料庫（按引擎 dump）、面板設定、Docker volumes。
- 目的地：本地、**Google Drive**、**OneDrive**（面板內 OAuth 授權流程，token 加密存）；排程 + 保留份數；一鍵還原。

### 3.14 檔案管理器 / Web Terminal / Cron / 監察
- **檔案**：`os.Root` 沙盒、上傳（分塊續傳）、下載、改名、刪除入回收站、chmod/chown、Zip/Unzip/tar.gz（防 zip-slip）、Monaco 編輯器。
- **Terminal**：xterm.js ↔ WS ↔ PTY（主機）或 docker exec；開啟前二次驗證；SSH 常用命令收藏。
- **Cron**：Shell / PHP / URL / 備份 / 日誌切割；秒級；超時、並發策略；執行記錄。
- **監察**：gopsutil 2 秒採集 CPU（每核）、RAM、磁碟 IO/空間、網絡速率、Load；服務健康（HAProxy、Docker、Nginx、DB、Redis、Varnish、CrowdSec）；uPlot 實時圖；閾值通知。

### 3.15 MCP
- `/mcp`（Streamable HTTP，API Token + scope：read / operate / admin）+ `lmcwhm mcp --stdio`。
- Tools 包裝 service 層：`list_sites`、`create_site`、`site_logs`、`analyze_logs`、`docker_restart`、`server_stats`、`ban_ip`（可揀 nftables/CF/Linode）、`issue_cert`、`run_backup`…；破壞性 tool 標 `destructiveHint`，全部入審計。

### 3.16 多語言
- `zh-HK`（預設）、`zh-TW`、`zh-CN`、`en`、`ja`；前端 vue-i18n 懶加載；後端只回錯誤碼 + 參數；CLI `lmcwhm` 同樣跟系統設定語言。

---

## 3A. 對照 cPanel / aaPanel / 寶塔 / 1Panel 後補充嘅功能（之前遺漏）

| # | 功能 | 參考來源 | 說明 / 落地 |
|---|---|---|---|
| 1 | **首頁儀表板** | 全部 | 系統概覽、資源圓環、網站/DB/FTP 數量、安全風險提示、快捷入口、可拖動卡片 |
| 2 | **WordPress Toolkit** | aaPanel WP Toolkit、Softaculous | WP 一鍵安裝、WP Sets（預設插件/主題組合）、克隆/Staging、插件/主題批量更新、核心完整性檢查、安全加固（禁 XML-RPC、改登入路徑） |
| 3 | **進程守護 (Supervisor)** | 寶塔進程守護管理器 | Node/Python/Java/自訂命令常駐、自動重啟、日誌；Runtime 項目底層用佢 |
| 4 | **FTP / SFTP 帳戶** | 寶塔、aaPanel、cPanel | Pure-FTPd（App Store）+ 受限 SFTP chroot 帳戶，綁定網站目錄、配額、限 IP |
| 5 | **Composer / Tomcat** | 寶塔、aaPanel | PHP 站點內一鍵 composer install/update；Java Runtime 支援 Tomcat 8/9/10 部署 war |
| 6 | **網站進階** | cPanel、寶塔 | 自訂錯誤頁（404/500…）、目錄列表 (Indexes) 開關、訪問限制（IP/UA/國家，配合 GeoIP）、流量/並發/速率限制、默認站點、防跨站 (open_basedir)、子目錄綁定 |
| 7 | **網站監控報表** | 寶塔網站監控報表、cPanel Metrics | 按站點統計 PV/UV/IP/頻寬/狀態碼/蜘蛛/來源/熱門 URL/地區分佈（解析 access log 入 SQLite 聚合表） |
| 8 | **網站可用性監測 (撥測)** | 寶塔拨测、1Panel 網站監測 | HTTP/HTTPS/TCP/Ping 定時探測、SSL 到期、關鍵字檢查、響應時間圖 → 通知 |
| 9 | **網站防篡改 / 檔案保護** | 寶塔文件防篡改、aaPanel File Protection | inotify/fanotify 監察網站目錄，保護目錄禁寫或改動即還原 + 告警 |
| 10 | **木馬 / Webshell 掃描** | 寶塔文件管理器 | 規則 + YARA + ClamAV（App Store）掃描網站目錄，顯示風險檔案 |
| 11 | **檔案管理補充** | aaPanel、寶塔 | 檔案內容全文搜尋、外鏈分享（有效期 + 密碼）、回收站、收藏夾、遠端下載 (URL → 伺服器) |
| 12 | **安全風險掃描 / 安全報告** | 寶塔首頁風險、1Panel 安全運維報表 | 基線檢查（SSH 弱設定、root 登入、弱密碼、開放端口、面板預設設定、PHP 漏洞版本、未更新套件）→ 評分 + 一鍵修復 + 定期 PDF/HTML 報告 |
| 13 | **工具箱 (Toolbox)** | 1Panel 工具箱 | Swap 管理、主機名、DNS 伺服器、時區/NTP、SSH 管理（端口、root 登入、密碼/金鑰登入開關、金鑰管理）、Fail2ban（可由 CrowdSec 取代，二揀一）、進程管理器、網絡連接列表、磁碟管理（掛載/分區/使用分析）、系統垃圾清理 |
| 14 | **面板快照 & 遷移** | 1Panel 快照 | 一鍵快照（面板設定 + App + 網站 + DB）上傳 Google Drive/OneDrive，喺新機器還原 = 整機搬遷 |
| 15 | **告警中心** | 1Panel、寶塔 | 統一規則：CPU/RAM/磁碟閾值、服務掛咗、SSL 到期、撥測失敗、登入異常、備份失敗 → Email/Telegram/Webhook/企業微信/釘釘 |
| 16 | **面板子用戶（非租戶）** | aaPanel Sub accounts | 只係面板操作員帳號 + 模組級權限（例如只可管網站 A/B、唔可用 Terminal），**唔涉及套餐/配額/Reseller**，保持單機管理模式 |
| 17 | **郵件伺服器（可選插件）** | aaPanel Mail Server、cPanel Email Deliverability | App Store 插件（Stalwart 或 Postfix+Dovecot+Rspamd）；自動經 CF 加 MX/SPF/DKIM/DMARC；寄信能力檢測（SPF/DKIM/PTR 檢查）|
| 18 | **DNS Zone Editor 完整版** | cPanel Zone Editor | A/AAAA/CNAME/MX/TXT/SRV/CAA/NS 全類型（經 Cloudflare），模板（Google Workspace / M365 一鍵加記錄） |
| 19 | **AI 功能** | 寶塔 AI、1Panel AI | 除 MCP 外：頁面上「一鍵 AI 分析」按鈕（日誌/錯誤/安全報告）、Skills（運維/WAF/郵件技能包，寫成 MCP prompts/resources）、App Store「AI」分類（Ollama、Open WebUI、n8n、Dify 等）|
| 20 | **多 Web Server（後期）** | aaPanel Multi-WebServer | 預設 Nginx；後期 Runtime 加 OpenResty / Apache / OpenLiteSpeed 選項（Edge 層唔受影響） |
| 21 | **多機管理（後期）** | 寶塔多機、1Panel 多節點 | 見 §2.3 Agent 模式 |
| 22 | **PWA 手機版** | aaPanel mobile、1Panel mobile | 前端響應式 + PWA，手機睇監控、收告警、啟停服務 |
| 23 | **API Token 管理 + OpenAPI** | cPanel API Tokens、1Panel API | 多 Token、scope、到期、IP 白名單；自動生成 OpenAPI 3 文檔 (`/api/docs`) |
| 24 | **面板升級 / 回滾 / 離線安裝** | 1Panel、寶塔 | 穩定/測試通道、更新前自動備份、失敗回滾；離線安裝包（內含 images） |

## 2.3 架構補充
- **插件系統**：App Store manifest 分三類 — `app`（Docker compose 應用）、`runtime`（網站語言環境）、`plugin`（擴展面板功能：前端掛載點 + 後端以獨立進程經 gRPC/unix socket 註冊 API，例如郵件伺服器、防篡改）。第三方插件需簽名校驗。
- **App Store 倉庫**：Git/HTTP 倉庫（似 1Panel appstore repo）+ 本地自訂應用；支援離線同步。
- **事件總線**：內部 pub/sub（site.created、cert.renewed、ip.banned…），告警中心、審計、MCP 通知全部訂閱事件，模組間解耦。
- **Agent 模式（多機預留）**：`lmcwhm` 可用 `--role=master|agent` 啟動；agent 經 mTLS gRPC 向 master 註冊，master UI 切換節點。M0 起所有 service 都經 `NodeClient` 介面呼叫（本機實作 = 直接呼叫），日後加遠端實作唔使改上層。
- **目錄規範**：`/opt/lmcwhm/{bin,data(db),apps,runtimes,backup,logs,certs,geoip}`、網站 `/www/wwwroot/<domain>`（沿用寶塔習慣，方便用戶遷移）；設定檔集中 `/opt/lmcwhm/data/conf/`。
- **寶塔/aaPanel 遷移工具（後期）**：讀取 `/www/server/panel` 站點/DB 資料匯入。

---

## 4. 專案目錄結構
```
whm/
├── cmd/lmcwhm/main.go         # serve / CLI 子命令 / 選單 / mcp --stdio
├── internal/
│   ├── api/ auth/ settings/   # 面板設定（端口/入口/SSL/2FA/授權IP）
│   ├── edge/                  # HAProxy cfg 生成 + Runtime API
│   ├── site/ runtime/{php,java,node,python,html}/
│   ├── db/{mysql,pgsql,mongo,mssql,redis,sqlite}/
│   ├── appstore/ docker/
│   ├── cert/ dns/ firewall/{nft,linode,cloudflare}/ security/{waf,crowdsec}/
│   ├── logs/ geoip/ backup/{local,gdrive,onedrive}/
│   ├── files/ terminal/ cron/ monitor/ notify/ mcp/ i18n/ store/
├── catalog/  images/  migrations/
├── deploy/install.sh          # 一鍵安裝（似 aaPanel install.sh）
└── web/                       # Vue 3 + Naive UI + vue-i18n + Monaco + xterm.js + uPlot
```

---

## 4A. M1 實作備註（與原設計嘅差異）
- **HAProxy 動態更新**：用「由資料庫重新生成整份設定 → `haproxy -c` 驗證 → master-worker 熱重載（USR2）」取代 Runtime API 逐條改 map。效果同樣唔斷線，但更簡單、可自動還原；Runtime API 留待需要極高頻改動時先加。
- **Docker**：M1 經 `docker` CLI 操作（安裝腳本必定提供）；M3 Docker 管理中心需要即時統計 / 日誌串流時再加 Engine API。
- **IPv4 / IPv6**：Edge 分開 `bind 0.0.0.0` 同 `bind ::: v6only`，避免 IPv4 客戶 IP 變成 `::ffff:` 格式（影響日誌、IP 歸屬地、CrowdSec）。
- **PHP**：每版本一個 PHP-FPM 容器（host network，監聽 `127.0.0.1:90xx`），官方 image 加 mysqli / pdo_mysql / opcache / bcmath / exif；其他擴展喺 M2 軟件設定入面逐個裝。
- **CI**：`deploy/test-sites.sh` 喺 Ubuntu runner 用真 Docker 測試建站、SNI、IPv6、PHP 切換、強制 HTTPS，並用 Pebble（Let's Encrypt 官方測試 CA）測試 ACME HTTP-01。DNS-01 / Cloudflare 只有單元測試（需要真 Cloudflare 帳戶）。

## 5. 路線圖
| 階段 | 內容 | 完成標準 |
|---|---|---|
| **M0** | Go 骨架、SQLite、登入、**面板設定（端口/安全入口 /console/面板 SSL/2FA/授權 IP）**、`lmcwhm` CLI、i18n、Vue 外殼、install.sh | 裝完顯示地址+入口，非入口路徑 404；`lmcwhm` 可改端口/密碼 |
| **M1** | HAProxy 443 SNI + 80 Host 雙棧、CF DNS、LE/LiteSSL、PHP 站點 | 面板加域名 → HTTPS v4/v6 可訪問 |
| **M2** | App Store、Runtime 插件（Java/Node/Python/HTML）、DB 管理（全引擎） | 各類型項目 + 各 DB 可建立 |
| **M3** | Docker 管理、檔案管理器、Terminal | 容器綁域名可用；穿越測試通過 |
| **M4** | 日誌中心分類 + IP 歸屬地、Cron、監察 | 全部分類可查、IP 顯示地區 |
| **M5** | 防火牆（nftables + Linode + CF）、WAF、CrowdSec | SQLi 被攔、IP 同步封鎖到 Linode |
| **M6** | 備份（本地/Google Drive/OneDrive） | 排程備份上傳 + 還原成功 |
| **M7** | MCP + AI 一鍵分析 + Skills | Claude Code 經 MCP 操作 |
| **M8** | 儀表板完善、WP Toolkit、Supervisor、FTP/SFTP、Composer/Tomcat、網站進階、網站報表、撥測、告警中心 | 3A #1–8、#15 |
| **M9** | 防篡改、木馬掃描、安全風險掃描報告、工具箱、面板子用戶、API Token/OpenAPI | 3A #9–13、#16、#23 |
| **M10** | 面板快照遷移、升級/回滾/離線安裝、PWA、郵件插件、Zone Editor 模板 | 3A #14、#17、#18、#22、#24 |
| **後期** | 多 Web Server、多機 Agent、寶塔遷移工具 | 3A #20、#21 |

批准後由 **M0** 開始實作。

---

## 6. 驗證
- Ubuntu 20.04 VM（Multipass，開 IPv6）+ 22.04 兼容測；真 Linode（測試用 Firewall）驗 Linode API；測試 CF zone。
- Edge：`haproxy -c`；`openssl s_client -servername`；`curl -6 --resolve`；Runtime API 加 map 免 reload 生效；port 80 → 301。
- 面板設定：改端口/入口後舊路徑 404；`lmcwhm entry --disable` 救援成功。
- `go test ./...`（edge 生成 golden、files 穿越/zip-slip、geoip 查詢、firewall 規則 diff）；`golangci-lint`。
- 前端 Vitest + Playwright（建站、語言切換、設定頁）。
- 備份：Google Drive / OneDrive 測試帳戶上傳 + 還原。
- MCP：MCP Inspector + `claude mcp add` 實測。
