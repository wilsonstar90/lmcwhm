-- Panel sub-users (not tenants): module permissions and an optional site scope.
ALTER TABLE users ADD COLUMN perms   TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE users ADD COLUMN sites   TEXT    NOT NULL DEFAULT '[]';
ALTER TABLE users ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN remark  TEXT    NOT NULL DEFAULT '';

-- Security baseline scans.
CREATE TABLE security_scans (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at INTEGER NOT NULL,
    score      INTEGER NOT NULL,
    findings   TEXT NOT NULL,
    hostname   TEXT NOT NULL DEFAULT '',
    millis     INTEGER NOT NULL DEFAULT 0
);

-- Webshell / malware scans.
CREATE TABLE malware_scans (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    paths       TEXT NOT NULL,
    state       TEXT NOT NULL,
    files       INTEGER NOT NULL DEFAULT 0,
    findings    TEXT NOT NULL DEFAULT '[]',
    error       TEXT NOT NULL DEFAULT '',
    clamav      INTEGER NOT NULL DEFAULT 0,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE malware_ignore (
    sha256     TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

-- Website tamper protection.
CREATE TABLE tamper_rules (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    site_id    INTEGER NOT NULL DEFAULT 0,
    path       TEXT NOT NULL UNIQUE,
    mode       TEXT NOT NULL,              -- lock | watch
    exts       TEXT NOT NULL DEFAULT '[]',
    exclude    TEXT NOT NULL DEFAULT '[]',
    restore    INTEGER NOT NULL DEFAULT 1,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL
);
CREATE TABLE tamper_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id    INTEGER NOT NULL,
    path       TEXT NOT NULL,
    op         TEXT NOT NULL,
    action     TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_tamper_events_rule ON tamper_events(rule_id, id);

-- File manager: share links and favourites.
CREATE TABLE file_shares (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    token         TEXT NOT NULL UNIQUE,
    path          TEXT NOT NULL,
    password_hash TEXT NOT NULL DEFAULT '',
    expires_at    INTEGER NOT NULL DEFAULT 0,
    max_downloads INTEGER NOT NULL DEFAULT 0,
    downloads     INTEGER NOT NULL DEFAULT 0,
    created_by    TEXT NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL
);
CREATE TABLE file_favorites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    path       TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    UNIQUE(user_id, path)
);

-- Mail server domains (mailboxes and aliases live in the mail container's config).
CREATE TABLE mail_domains (
    name       TEXT PRIMARY KEY,
    created_at INTEGER NOT NULL
);
