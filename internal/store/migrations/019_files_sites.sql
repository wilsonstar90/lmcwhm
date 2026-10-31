-- File remarks (shown in the file manager next to each path).
CREATE TABLE file_remarks (
    path       TEXT PRIMARY KEY,
    remark     TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Databases dropped through the panel, kept as a dump until restored or purged.
CREATE TABLE db_trash (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    app_id     INTEGER NOT NULL,
    engine     TEXT NOT NULL,
    name       TEXT NOT NULL,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL, -- sealed
    remark     TEXT NOT NULL DEFAULT '',
    file       TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    deleted_at INTEGER NOT NULL
);

-- Website settings like aaPanel: what a stopped site answers, expiry,
-- category, run directory, rewrite rules, index files, custom nginx
-- directives, and the subdirectory a bound domain serves.
ALTER TABLE sites ADD COLUMN stop_mode TEXT NOT NULL DEFAULT '404';
ALTER TABLE sites ADD COLUMN stop_page INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN category TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN run_dir TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN rewrite TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN index_files TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN custom_conf TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN target_subdir TEXT NOT NULL DEFAULT '';
