-- Remarks on scheduled tasks (and backup plans, which are scheduled tasks),
-- and on Docker containers and compose projects, which live outside the
-- panel's tables: keyed by name, as a container's id changes when recreated.
ALTER TABLE cron_jobs ADD COLUMN remark TEXT NOT NULL DEFAULT '';

CREATE TABLE docker_remarks (
    kind       TEXT NOT NULL, -- container | project
    name       TEXT NOT NULL,
    remark     TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (kind, name)
);
