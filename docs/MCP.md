# MCP：用 Kimi、Claude、Codex 管理 lmcwhm

lmcwhm 內建 MCP server，支援 **Streamable HTTP** 及 **SSH stdio**。常用功能有專用工具；`list_api_endpoints` + `call_api` 亦會暴露面板實際註冊的 REST 路由，因此網站、SSL、App Store、資料庫、Docker、檔案、Cron、安全、備份、撥測、WordPress、郵件、面板設定等都可由 AI 操作。

## 1. 準備連線

在面板「設定 → API tokens」建立 Token：

| Scope | 權限 |
|---|---|
| `read` | 唯讀工具及非管理路由的 GET |
| `operate` | 加上一般網站、Docker、資料庫、安全、備份等操作；不可修改面板管理設定 |
| `admin` | 全部功能，包括面板管理及 root shell |

可按需要設定來源 IP 及到期日。HTTP URL 是 `https://伺服器:面板port/mcp`，**不要**加安全入口 `/console`。建議使用有效 TLS 憑證；若客戶端不接受自簽憑證，請改用 SSH stdio，不要關閉憑證驗證。

介面「AI 助手 → MCP」亦提供可複製的連線 Prompt，會自動帶入目前 MCP URL；貼畀 Kimi Code、Claude Code 或 Codex 後，AI 會按目前客戶端協助配置及驗證連線。

把 Token 放進啟動客戶端前的環境，不要提交到 Git：

```bash
export LMCWHM_TOKEN='lmc_...'
```

## 2. Kimi Code

編輯 `~/.kimi-code/mcp.json`，或在專案使用 `.kimi-code/mcp.json`：

```json
{
  "mcpServers": {
    "lmcwhm": {
      "url": "https://server.example.com:8888/mcp",
      "bearerTokenEnvVar": "LMCWHM_TOKEN",
      "toolTimeoutMs": 900000
    }
  }
}
```

SSH stdio（不需 API Token，但 SSH root 身份等同 `admin`）：

```json
{
  "mcpServers": {
    "lmcwhm": {
      "command": "ssh",
      "args": ["root@server.example.com", "lmcwhm", "mcp", "--stdio"]
    }
  }
}
```

新增設定後開新 session，再用 `/mcp` 檢查連線。

## 3. Claude Code

```bash
claude mcp add --transport http lmcwhm https://server.example.com:8888/mcp \
  --header "Authorization: Bearer $LMCWHM_TOKEN"
```

或經 SSH，不在 Claude 設定保存 Token：

```bash
claude mcp add lmcwhm -- ssh root@server.example.com lmcwhm mcp --stdio
```

用 `claude mcp list` 檢查連線。

## 4. Codex

編輯 `~/.codex/config.toml`：

```toml
[mcp_servers.lmcwhm]
url = "https://server.example.com:8888/mcp"
bearer_token_env_var = "LMCWHM_TOKEN"
tool_timeout_sec = 900
default_tools_approval_mode = "writes"
```

SSH stdio：

```toml
[mcp_servers.lmcwhm]
command = "ssh"
args = ["root@server.example.com", "lmcwhm", "mcp", "--stdio"]
tool_timeout_sec = 900
default_tools_approval_mode = "writes"
```

用 `codex mcp list` 或 TUI 的 `/mcp` 檢查。

## 5. 工具用法

優先使用語意明確的專用工具，例如 `server_overview`、`list_sites`、`issue_certificate`、`container_action`、`ban_ip`、`run_backup`。背景工作會回傳 `task_id`，再用 `get_task` 查進度。

找不到專用工具時：

1. 用 `list_api_endpoints` 搜尋，例如 `{"module":"database"}` 或 `{"search":"mail"}`。
2. 把路由中的 `{id}`、`{domain}` 等換成實際值。
3. 用 `call_api` 調用；`path` 可寫 `/sites` 或 `/api/v1/sites`。

```json
{"path": "/system/summary"}
```

```json
{"path": "/logs/site", "query": {"domain": "example.com", "kind": "error", "lines": "200"}}
```

```json
{"method": "POST", "path": "/cron/12/run", "body": {}}
```

回應包含 HTTP `status`、`ok` 及 JSON `body`。API 錯誤會保留原狀態碼及穩定錯誤碼。每次工具調用都寫入操作日誌；`call_api` 只記錄 method 和 path，不記錄可能含密碼的 body。

## 6. 安全及限制

- 先以 `read` Token 驗證連線，只給自動化流程所需的最小 scope。
- `operate`、`admin`、`call_api` 的非 GET 調用及 `run_shell` 可改變真實 Server；保持客戶端的人工審批。
- MCP 每次調用使用現有 handler 的輸入驗證、業務邏輯及模組權限，不是繞過 API 的 shell 模擬。
- `call_api` 適合 JSON API。大型下載會截斷；binary 下載及互動 Web Terminal／WebSocket 應使用面板、CLI 或 SSH。
- 撤銷外洩 Token 可在面板刪除並重新建立；stdio 的安全性取決於 SSH 帳號及 sudo/root 權限。

客戶端官方說明：[Kimi Code MCP](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html)、[Claude Code MCP](https://docs.anthropic.com/en/docs/claude-code/mcp)、[Codex MCP](https://developers.openai.com/codex/mcp)。
