-- API tokens for MCP clients (only a SHA-256 of the token is stored).
CREATE TABLE api_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    hint       TEXT NOT NULL,
    scope      TEXT NOT NULL,              -- read | operate | admin
    ips        TEXT NOT NULL DEFAULT '[]', -- JSON list of IPs / CIDRs; empty = any
    expires_at INTEGER NOT NULL DEFAULT 0,
    last_used  INTEGER NOT NULL DEFAULT 0,
    last_ip    TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);
