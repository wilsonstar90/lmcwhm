-- User-Agent and URL black / white lists (黑白名單), enforced by the edge.
CREATE TABLE fw_patterns (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,              -- ua | url
    list       TEXT NOT NULL,              -- block | allow
    match      TEXT NOT NULL,              -- contains | prefix | regex
    value      TEXT NOT NULL,
    remark     TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL,
    UNIQUE(kind, list, match, value)
);
