-- Website protection (網站防護): switches per site, as JSON.
CREATE TABLE site_protect (
    domain   TEXT PRIMARY KEY,
    settings TEXT NOT NULL
);

-- Dedicated rules (專屬規則): built-in ones carry a code ("wp-1002").
CREATE TABLE waf_rules (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    code        TEXT NOT NULL UNIQUE,
    cms         TEXT NOT NULL DEFAULT '',
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    builtin     INTEGER NOT NULL DEFAULT 0,
    match_all   INTEGER NOT NULL DEFAULT 1,
    conditions  TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);
