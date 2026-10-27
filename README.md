# lmcwhm

自研伺服器控制面板（aaPanel / 寶塔式），Go 單一 Binary + Vue 3 / Naive UI。
完整架構與路線圖：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## 目前進度：M1（SNI 邊緣分流 + 網站）

**M1 — 網站管理**
- HAProxy 3.0 邊緣：80 / 443（IPv4 + IPv6 分開監聽，客戶真實 IP 正確傳遞），按 TLS SNI 分流：
  需要直通嘅域名原封轉發（passthrough，M3 Docker 應用會用），其餘喺本機終止 TLS，每個域名自動揀返自己張證書
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
make test       # go vet + go test
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

CLI 直接讀寫 `/opt/lmcwhm/data/panel.db`，面板無運行都可以救援。
