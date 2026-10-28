# lmcwhm

自研伺服器控制面板（aaPanel / 寶塔式），Go 單一 Binary + Vue 3 / Naive UI。
完整架構與路線圖：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## 目前進度：M5（防火牆、WAF、CrowdSec）

**M5 — 安全**
- 伺服器防火牆（nftables，獨立表 `inet lmcwhm`，唔會改 ufw / Docker 規則）：IP 黑名單 / 白名單（CIDR、IPv4 / IPv6、永久或限時）、端口規則（TCP / UDP、範圍、來源）、Ping 開關、受保護端口（SSH、面板、80、443）；「預設拒絕」要 60 秒內確認仲連得到面板，否則自動還原；救援：`lmcwhm firewall-off`
- 封鎖同步到 Cloudflare（每個 Zone 嘅 IP Access Rules，另可改安全等級 / Under Attack）同 Linode 雲防火牆
- WAF：ModSecurity v3 + OWASP CRS 4.25 LTS（攔截 / 只偵測、偏執等級、異常分數門檻），每個網站可以單獨關閉或者排除規則，攔截記錄可以一鍵排除
- CrowdSec：分析網站同 SSH 日誌，封鎖決定由面板寫入防火牆；可選社群黑名單、警報、手動封鎖 / 解封

**M4 — 監察 / 計劃任務 / 日誌**
- 監察：CPU、記憶體、負載、磁碟 I/O、網絡（15 分鐘至 30 日）、資源用得最多嘅進程、面板服務狀態
- 計劃任務：Shell（root）、訪問 URL、網站日誌切割；可用秒級週期、預覽之後幾次執行時間、上次未完成會略過、保留每次輸出；有範本（日誌切割、清理 Docker 映像、系統更新）
- 日誌中心：面板操作 / 登入、SSH 登入（失敗最多嘅 IP）、網站訪問（解析成表格）/ 錯誤日誌（搜尋、下載、清空）、容器、系統 journal、計劃任務
- IP 歸屬地：每個 IP 旁邊顯示國旗、地區、ASN（DB-IP Lite，每月自動更新）

**域名 / SNI 管理 + 更新 / 修復**
- 「域名 / SNI」頁：列出 443 / 80 上每個域名同佢連去邊（網站、Docker 容器、伺服器、TLS 直通），並檢查證書、DNS 同上游；有路由測試；可以加子域名 / 通配符
- 邊緣路由：完全匹配優先於通配符，較深嘅通配符優先於較淺嘅；修正咗直通域名誤中子域名嘅問題
- 首頁「更新」（檢查 GitHub 最新版）同「修復」（重裝目前版本，可以重建網站環境容器）；網站、資料庫、設定全部保留

**M3 — Docker 管理 / 檔案管理器 / Terminal**
- Docker：容器列表（即時 CPU / 記憶體 / 網絡）、建立（端口、環境變數、掛載、重啟策略、資源限制）、啟停 / 暫停 / 刪除、日誌、詳細資料、`docker exec` 終端；Images（拉取 / 刪除 / 清理）、Volumes、網絡；Compose 項目（網頁編輯 yml + .env，up / down / restart / pull、日誌）
- 容器「綁定域名」＝建立反向代理網站（`http://127.0.0.1:端口`），自動 HAProxy 路由 + SSL + DNS；亦可揀 **TLS 直通**（按 SNI 原封轉發，由後端自己持證書）
- 面板自己嘅容器（`lmcwhm.managed`）唔可以喺 Docker 頁刪除
- 檔案管理器：全部操作經 Go `os.Root`，路徑（包括符號連結）唔可以走出根目錄；瀏覽、編輯（5 MB 內文字檔）、新增、改名、複製 / 移動、權限 / 擁有者（可遞歸）、zip / tar.gz 壓縮及解壓（防 zip-slip）、8 MB 分塊上傳（可續傳）、下載、回收站（`/www/.Recycle_bin`，可還原）
- Web Terminal：xterm.js ↔ WebSocket ↔ PTY，主機 root shell 或容器 shell；每次開啟前要再輸入面板密碼

**M2 — App Store / 運行環境 / 資料庫**
- App Store（每個版本獨立安裝）：Node.js 18–24、Python 3.9–3.13、Java（Temurin JDK 8/11/17/21）、MySQL 5.7/8.0/8.4、MariaDB 10.6/10.11/11.4、PostgreSQL 13–17、MongoDB 6/7、SQL Server 2019/2022、Redis 6.2/7.2/7.4、Memcached、Adminer
- 資料庫 / 快取以容器運行，只發佈到 `127.0.0.1:端口`；管理員密碼加密儲存；MySQL / MariaDB / Redis 設定檔可網頁編輯，保存後重啟，起唔返自動還原
- 網站新增 **Node.js / Python / Java 項目**（專用容器運行啟動命令，監聽 `127.0.0.1:PORT`，Nginx 反代，支援 WebSocket）同 **反向代理** 類型；項目狀態、輸出日誌、重啟
- 資料庫管理：MySQL、MariaDB、PostgreSQL、MongoDB、SQL Server 一鍵建立資料庫 + 專屬用戶、改密碼、刪除；Adminer 經面板 `/adminer/` 打開（需已登入面板）

**M1 — 網站管理**
- HAProxy 3.0 邊緣：80 / 443（IPv4 + IPv6 分開監聽，客戶真實 IP 正確傳遞），按 TLS SNI 分流：
  需要直通嘅域名原封轉發（passthrough），其餘喺本機終止 TLS，每個域名自動揀返自己張證書
- Nginx + PHP-FPM 7.4 / 8.0 / 8.1 / 8.2 / 8.3 / 8.4（Docker 容器，每版本一個，內置 mysqli / pdo_mysql / opcache / bcmath / exif），亦支援靜態網站
- 網站：新增、別名、切換 PHP 版本、停止 / 啟動、刪除（可選刪檔案 + DNS）、強制 HTTPS、HSTS、睇 Nginx 設定
- Cloudflare：API Token 加密儲存，自動建立 / 更新 / 清理 A（IPv4）+ AAAA（IPv6）記錄，可選橙雲代理
- SSL：Let's Encrypt、LiteSSL 或任何 ACME CA（支援 EAB）；HTTP-01 經 port 80，或 DNS-01 經 Cloudflare（支援萬用字元）；每日自動續期（到期前 30 日）；可上傳自訂證書
- 所有設定由資料庫生成，改動先驗證（`nginx -t`、`haproxy -c`）再熱重載，失敗自動還原；面板重啟 / 修復會重新生成
- 網站檔案：`/www/wwwroot/<域名>`，日誌：`/www/wwwlogs/`

**M0 — 面板核心**
- 面板端口（改完即時熱重啟監聽）、安全入口（如 `/console`，未經入口一律 404）、面板 SSL、授權 IP、綁定域名、BasicAuth
- 登入：bcrypt、TOTP 兩步驗證、按 IP 失敗鎖定、HttpOnly session + CSRF
- 面板登入 / 操作日誌、首頁儀表板、5 種語言、`lmcwhm` CLI（似 `bt`）

> 使用網站功能前：Web 環境會佔用 80 / 443 端口（VPS 上唔可以有其他 Nginx / Apache 監聽）；
> 喺「網站 → Web 環境」設定 ACME 電郵（同埋 Cloudflare Token 如需自動 DNS / DNS-01）。

## 開發

```bash
make web        # 建置前端（web/dist，會 embed 入 binary）
make dev-init   # 喺 ./.dev 初始化：admin / admin12345，端口 18888，入口 /console
make dev        # 啟動面板：https://127.0.0.1:18888/console
make test       # go vet + go test（包括檔案穿越 / zip-slip 測試）
make linux      # 交叉編譯 bin/lmcwhm-linux-{amd64,arm64}
```

前端熱更新：`make dev` 運行中，另開 `cd web && npm run dev`，瀏覽 `http://localhost:5173/console`。

## 安裝到 VPS（Ubuntu 20.04 / 22.04 / 24.04）

以 root 喺 VPS 執行：

```bash
curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash
```

自訂參數（端口 / 安全入口 / 帳號）：

```bash
curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash -s -- --port 8888 --entry /console --username admin
```

其他選項：`--password xxx`、`--version v0.1.0`（指定版本）、`--skip-docker`、`--binary ./lmcwhm-linux-amd64`（用本地編譯檔）。
安裝完會顯示面板地址、安全入口、用戶名同密碼；記得喺 Linode Cloud Firewall / 安全組放行面板端口。

## 修復 / 強制更新

已安裝嘅機器重跑同一條安裝命令，會自動進入**修復/更新模式**：

```bash
curl -fsSL https://raw.githubusercontent.com/wilsonstar90/lmcwhm/main/deploy/install.sh | sudo bash
```

| 重建 | 保留（永遠唔刪） |
|---|---|
| 面板程式、systemd 服務、`/usr/bin/lmcwhm`；Docker 壞咗會重啟 / 重裝套件（容器同數據 volume 保留） | 網站檔案 `/www/wwwroot`、面板設定同帳號（`/opt/lmcwhm/data`）、證書、所有防火牆規則（ufw 只會加唔會刪） |

流程：先備份 `data/` 去 `/opt/lmcwhm/backup/reinstall-<時間>.tar.gz`（保留最近 5 份）→ 換程式 → 資料庫 migration → 重啟 → 健康檢查；新版起唔到會**自動還原**舊程式同數據。

喺 VPS 亦可以直接用：

| 命令 | 作用 |
|---|---|
| `sudo lmcwhm update` | 更新到最新版（`--version v0.1.1` 指定版本） |
| `sudo lmcwhm repair` | 重新安裝目前版本，修復被改壞嘅程式 / 服務 |
| `sudo lmcwhm reset-panel` | 鎖死自己入唔到面板時用：新安全入口 + 新管理員密碼、自簽 SSL、關閉 2FA / 授權 IP / 綁定域名 / BasicAuth；**端口保持不變**（可加 `--port`） |

`update` / `repair` 都可以加 `--reset-panel`；安裝命令亦可：`... | sudo bash -s -- --reset-panel`。

## 發佈新版本

推送 `v*` tag，GitHub Actions 會自動建置前端 + Linux amd64/arm64 binary 並建立 Release（安裝腳本預設下載最新 Release）：

```bash
git tag v0.1.0
git push origin v0.1.0
```

## `lmcwhm` 命令

直接輸入 `lmcwhm` 顯示數字選單；或者用子命令：

| 命令 | 作用 |
|---|---|
| `lmcwhm default` | 顯示面板地址、安全入口、用戶名 |
| `lmcwhm start\|stop\|restart\|status` | 控制 systemd 服務 |
| `lmcwhm reload` | 重新載入設定（SIGHUP，免重啟） |
| `lmcwhm passwd [新密碼]` / `username [新用戶名]` | 修改管理員帳號 |
| `lmcwhm port [新端口]` | 顯示 / 修改面板端口 |
| `lmcwhm entry [/path]` / `entry --disable` | 修改 / 關閉安全入口 |
| `lmcwhm ssl-off` | 關閉面板 SSL |
| `lmcwhm 2fa-off` | 關閉兩步驗證 |
| `lmcwhm domain-unbind` / `ip-clear` / `basicauth-off` | 解除域名綁定 / 授權 IP / BasicAuth |
| `lmcwhm unlock` | 解除登入失敗鎖定 |
| `lmcwhm log` | 面板日誌最後 100 行 |
| `lmcwhm update` / `repair` / `reset-panel` | 見上面「修復 / 強制更新」 |
| `lmcwhm firewall-off` | 關閉面板防火牆（鎖死自己時用；ufw / Docker 規則唔受影響） |

CLI 直接讀寫 `/opt/lmcwhm/data/panel.db`，面板無運行都可以救援。
