-- Alert center.
CREATE TABLE alert_channels (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,              -- email | telegram | webhook | wecom | dingtalk | slack | discord
    config     TEXT NOT NULL,              -- sealed JSON
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);
CREATE TABLE alert_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,
    params     TEXT NOT NULL DEFAULT '{}',
    channels   TEXT NOT NULL DEFAULT '[]', -- JSON list of channel ids
    enabled    INTEGER NOT NULL DEFAULT 1,
    cooldown   INTEGER NOT NULL DEFAULT 60, -- minutes between repeats
    created_at INTEGER NOT NULL
);
CREATE TABLE alert_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id    INTEGER NOT NULL,
    rule_name  TEXT NOT NULL,
    kind       TEXT NOT NULL,
    key        TEXT NOT NULL DEFAULT '',
    severity   TEXT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL,
    result     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

-- Uptime probes (撥測).
CREATE TABLE probes (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    type          TEXT NOT NULL,           -- http | tcp | ping
    target        TEXT NOT NULL,           -- URL | host:port | host
    interval_s    INTEGER NOT NULL DEFAULT 60,
    timeout_s     INTEGER NOT NULL DEFAULT 10,
    keyword       TEXT NOT NULL DEFAULT '',
    expect_status INTEGER NOT NULL DEFAULT 0, -- 0 = any 2xx/3xx
    fails         INTEGER NOT NULL DEFAULT 2, -- consecutive failures before "down"
    enabled       INTEGER NOT NULL DEFAULT 1,
    created_at    INTEGER NOT NULL
);
CREATE TABLE probe_results (
    probe_id   INTEGER NOT NULL,
    t          INTEGER NOT NULL,
    ok         INTEGER NOT NULL,
    latency_ms INTEGER NOT NULL,
    error      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX probe_results_t ON probe_results(probe_id, t);

-- Website statistics from nginx access logs.
CREATE TABLE site_stats_hourly (
    domain TEXT NOT NULL,
    hour   INTEGER NOT NULL,               -- unix time of the hour
    pv     INTEGER NOT NULL DEFAULT 0,
    bytes  INTEGER NOT NULL DEFAULT 0,
    s2xx   INTEGER NOT NULL DEFAULT 0,
    s3xx   INTEGER NOT NULL DEFAULT 0,
    s4xx   INTEGER NOT NULL DEFAULT 0,
    s5xx   INTEGER NOT NULL DEFAULT 0,
    spider INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (domain, hour)
);
CREATE TABLE site_stats_keys (
    domain TEXT NOT NULL,
    day    INTEGER NOT NULL,               -- unix time of the day (server time zone)
    kind   TEXT NOT NULL,                  -- ip | url | ref | spider | status
    key    TEXT NOT NULL,
    hits   INTEGER NOT NULL,
    bytes  INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (domain, day, kind, key)
);
CREATE TABLE site_stats_cursor (
    domain TEXT PRIMARY KEY,
    inode  INTEGER NOT NULL,
    offset INTEGER NOT NULL
);

-- Process guardian (進程守護): systemd services managed by the panel.
CREATE TABLE processes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    command    TEXT NOT NULL,
    dir        TEXT NOT NULL DEFAULT '/',
    user       TEXT NOT NULL DEFAULT 'root',
    env        TEXT NOT NULL DEFAULT '[]', -- JSON list of KEY=value
    remark     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

-- FTP (Pure-FTPd virtual users) and SFTP (chrooted system users) accounts.
CREATE TABLE ftp_accounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,              -- ftp | sftp
    username   TEXT NOT NULL UNIQUE,
    dir        TEXT NOT NULL,
    site_id    INTEGER NOT NULL DEFAULT 0,
    password   TEXT NOT NULL,              -- sealed
    enabled    INTEGER NOT NULL DEFAULT 1,
    remark     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL
);

-- Website advanced settings (JSON): directory listing, IP / UA rules, rate
-- limits, error pages, hotlink protection, open_basedir, XML-RPC block.
ALTER TABLE sites ADD COLUMN advanced TEXT NOT NULL DEFAULT '{}';
