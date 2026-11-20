-- Encrypted backups and isolated restore-drill history.
ALTER TABLE backups ADD COLUMN encrypted INTEGER NOT NULL DEFAULT 0;

CREATE TABLE backup_drills (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    backup_id   INTEGER NOT NULL REFERENCES backups(id) ON DELETE CASCADE,
    status      TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    started     INTEGER NOT NULL,
    finished    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX backup_drills_backup ON backup_drills(backup_id, id DESC);
