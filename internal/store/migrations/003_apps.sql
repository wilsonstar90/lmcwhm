-- App Store installs: one row per installed app version.
CREATE TABLE apps (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    app        TEXT NOT NULL,              -- catalog key: mysql | node | ...
    version    TEXT NOT NULL,
    port       INTEGER NOT NULL DEFAULT 0, -- loopback port of services (0 for runtimes)
    secret     TEXT NOT NULL DEFAULT '',   -- sealed admin password
    created_at INTEGER NOT NULL,
    UNIQUE(app, version)
);

CREATE TABLE databases (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id     INTEGER NOT NULL REFERENCES apps(id),
    name       TEXT NOT NULL,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL,              -- sealed
    remark     TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    UNIQUE(app_id, name)
);

-- Runtime projects (node | python | java) and reverse proxies.
ALTER TABLE sites ADD COLUMN runtime_version TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN command TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN app_port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN proxy_target TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN passthrough INTEGER NOT NULL DEFAULT 0;
