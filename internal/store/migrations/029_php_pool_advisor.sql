-- Hysteresis state for optional per-site PHP pool automation.
CREATE TABLE php_pool_advisor (
    site_id     INTEGER PRIMARY KEY REFERENCES sites(id) ON DELETE CASCADE,
    target      TEXT NOT NULL DEFAULT '',
    streak      INTEGER NOT NULL DEFAULT 0,
    updated     INTEGER NOT NULL DEFAULT 0,
    last_switch INTEGER NOT NULL DEFAULT 0
);
