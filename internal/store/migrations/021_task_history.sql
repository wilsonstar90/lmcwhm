-- Finished background tasks (installs, updates, builds, backups, ...) for
-- the header's message box; the log keeps the last lines only.
CREATE TABLE task_history (
    id       TEXT PRIMARY KEY,
    name     TEXT NOT NULL,
    status   TEXT NOT NULL,
    error    TEXT NOT NULL DEFAULT '',
    started  INTEGER NOT NULL,
    finished INTEGER NOT NULL,
    log      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX task_history_finished ON task_history(finished);
