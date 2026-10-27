# lmcwhm

自研伺服器控制面板（aaPanel / 寶塔式），Go 單一 Binary + Vue 3 / Naive UI。
完整架構與路線圖：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## 目前進度：M0（骨架 + 面板系統設定）

- 面板端口（改完即時熱重啟監聽，免停機）、IPv4/IPv6 雙棧監聽
- 安全入口（如 `/console`；未經入口一律回 nginx 風格 404）
- 面板 SSL：自簽 / 自訂證書 / 關閉
- 授權 IP（IP / CIDR，127.0.0.1 永遠放行方便救援）、綁定域名、BasicAuth
- 登入：bcrypt、TOTP 兩步驗證、按 IP 失敗鎖定、HttpOnly session + CSRF
- 日誌：面板登入日誌、面板操作（審計）日誌
- 首頁儀表板：CPU / 記憶體 / 負載 / 網絡 / 磁碟
- 多語言：zh-HK、zh-TW、zh-CN、en、ja（前端 + CLI）
- `lmcwhm` CLI（似 `bt`）

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

CLI 直接讀寫 `/opt/lmcwhm/data/panel.db`，面板無運行都可以救援。
