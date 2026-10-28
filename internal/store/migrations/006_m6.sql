-- Backups: one row per stored copy (local file or object on a remote).
CREATE TABLE backups (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,              -- site | database | panel | volume | path
    target     TEXT NOT NULL,              -- domain, database name, volume, directory
    target_id  INTEGER NOT NULL DEFAULT 0, -- database id
    name       TEXT NOT NULL,              -- archive file name
    size       INTEGER NOT NULL,
    dest       TEXT NOT NULL,              -- local | remote:<id>
    location   TEXT NOT NULL,              -- local path or remote file id
    job_id     INTEGER NOT NULL DEFAULT 0, -- cron job; 0 = manual
    created_at INTEGER NOT NULL
);
CREATE INDEX backups_target ON backups(kind, target, dest, created_at);

-- Cloud destinations (Google Drive / OneDrive); config is sealed JSON.
CREATE TABLE backup_remotes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,              -- gdrive | onedrive
    config     TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
