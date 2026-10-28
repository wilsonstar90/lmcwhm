-- Firewall: port rules and IP lists, applied as the nftables table "inet lmcwhm".
CREATE TABLE fw_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    action     TEXT NOT NULL,              -- accept | drop
    proto      TEXT NOT NULL,              -- tcp | udp | both
    ports      TEXT NOT NULL,              -- "22", "80,443", "8000-9000"
    source     TEXT NOT NULL DEFAULT '',   -- '' = anywhere, or IP / CIDR
    remark     TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);

CREATE TABLE fw_ips (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ip         TEXT NOT NULL,              -- IP or CIDR (normalised)
    list       TEXT NOT NULL,              -- block | allow
    source     TEXT NOT NULL DEFAULT 'manual', -- manual | crowdsec | waf
    remark     TEXT NOT NULL DEFAULT '',
    expires_at INTEGER NOT NULL DEFAULT 0, -- 0 = permanent
    created_at INTEGER NOT NULL,
    UNIQUE(ip, list)
);
CREATE INDEX fw_ips_expiry ON fw_ips(expires_at);

-- Per-site WAF: off switch and CRS rule IDs removed for false positives.
ALTER TABLE sites ADD COLUMN waf_off INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN waf_exclude TEXT NOT NULL DEFAULT ''; -- space-separated rule IDs
