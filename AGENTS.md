# AGENTS.md

## 項目目的

lmcwhm 係 Go 單一 Binary + Vue 3 嘅單機 Linux 管理面板。程式可以修改真實主機、Docker、nftables、systemd、網站、資料庫及備份，所以開發環境必須同使用中的 Server 隔離。

## 安全邊界

- 未經用戶明確授權，唔好 SSH 到其他機器，唔好執行 `deploy/install.sh`、`deploy/test-*.sh`、`systemctl`、`docker`、`nft`、`ufw`、更新、回滾或重啟命令。
- Linux E2E 只可在拋棄式 Ubuntu VM／CI runner 執行，唔可在生產或使用中的 Server 執行。
- 唔好讀取、複製或輸出 `.dev`、master key、API token、密碼、OAuth secret 或其他 credentials。
- 未經明確要求，唔好 push、commit、merge、rebase、落 tag、建立 Release、改版本號或升級 dependencies。
- 保留現有工作目錄及 branch；需要修改時，優先從指定 baseline 開獨立 worktree／branch。
- 唔好選擇或更改軟件 License；呢個決定由項目擁有者作出。

## 開發結構

- `cmd/lmcwhm/`：process entrypoint 及 CLI。
- `internal/server/`：HTTP API、權限、OpenAPI、MCP。
- `internal/store/migrations/`：SQLite migrations，只追加新編號，唔改已發佈檔案。
- `internal/site/`、`internal/webenv/`：網站及 Edge/Web 設定生成。
- `internal/appstore/`、`internal/database/`：應用及資料庫。
- `web/src/`：Vue views、components、stores、locales。
- `deploy/`：安裝及隔離 Ubuntu E2E。
- `README.md`、`docs/ARCHITECTURE.md`：目前功能及實際架構；改變行為時同步更新。

## 工作方式

- 跟隨現有 Go／Vue 命名及結構，唔好為小改動引入新 framework 或 dependency。
- API error 使用穩定 dotted code；前端負責翻譯。新增 API route 必須檢查 `internal/server/perms.go`，未映射 route 預設只限管理員。
- 高風險操作需要權限、輸入驗證、審計及可恢復策略。外部命令避免 shell string 拼接。
- 檔案及 archive 操作必須維持 `os.Root`、symlink、path traversal、zip-slip 防護。
- Secret 唔好寫入日誌、audit detail、API response 或測試輸出。
- Migration 必須考慮舊資料、upgrade 及 rollback；唔好假設全新資料庫。
- 修改功能時更新受影響測試及文件，唔好保留描述舊行為嘅 comments。

## 本機驗證

```bash
make test
cd web && npm run build
for f in ../deploy/*.sh; do bash -n "$f"; done
```

需要 build binary 時：

```bash
make build
make linux
```

`make web` 會執行 `npm ci`；純 TypeScript／production 驗證可在 dependencies 已存在時直接用 `npm run build`。

## CI 與交付

預設完整交付流程係：**本機驗證 → commit → push → 跟進 GitHub CI 全部 jobs → 建立新版本 tag → 跟進 Release workflow → 核對 amd64、arm64、SHA256SUMS、install.sh 及 `releases/latest`**。只 push 而未完成 CI 同 Release，唔算完成交付；實際更新或重啟 Server 仍需用戶明確確認。

- PR／push 必須保持 `ci.yml` 的 Go、frontend、Linux build 及 Ubuntu E2E 通過。
- 修改 source compile recipes 時，需要相關 `source-check.yml` 驗證。
- 本機 macOS 測試不能替代 Linux、systemd、Docker、nftables 或真實網站 E2E。
- 冇實際執行嘅外部整合或 Linux 測試要清楚列為未驗證，唔好當作完成。
- 交付前檢查 `git status` 及完整 diff，報告修改檔案、測試結果、未驗證範圍，並刪除今次產生而唔需要嘅測試輸出。
