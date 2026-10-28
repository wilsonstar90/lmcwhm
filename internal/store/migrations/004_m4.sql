-- Monitoring: one row per minute (averages of 5-second samples).
CREATE TABLE metrics (
    ts      INTEGER PRIMARY KEY, -- unix seconds, minute start
    cpu     REAL NOT NULL,       -- %
    mem     REAL NOT NULL,       -- %
    swap    REAL NOT NULL,       -- %
    load1   REAL NOT NULL,
    disk    REAL NOT NULL,       -- % used of /
    disk_r  REAL NOT NULL,       -- bytes/s
    disk_w  REAL NOT NULL,
    net_rx  REAL NOT NULL,
    net_tx  REAL NOT NULL
);

-- Cron jobs and their runs.
CREATE TABLE cron_jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,             -- shell | url | logrotate
    schedule    TEXT NOT NULL,             -- cron expression (optional seconds field) or @every …
    command     TEXT NOT NULL DEFAULT '',  -- script / URL / settings JSON
    enabled     INTEGER NOT NULL DEFAULT 1,
    timeout     INTEGER NOT NULL DEFAULT 3600, -- seconds
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE cron_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id      INTEGER NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
    started_at  INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    status      TEXT NOT NULL,             -- running | success | failed | timeout | skipped
    exit_code   INTEGER NOT NULL DEFAULT 0,
    output      TEXT NOT NULL DEFAULT '',
    manual      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX cron_runs_job ON cron_runs(job_id, id);
