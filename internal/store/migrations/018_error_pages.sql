-- Custom 4xx / 5xx pages (HTML with nginx SSI placeholders); built-in ones
-- carry a code ("blocked") and can be reset.
CREATE TABLE error_pages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    code       TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    html       TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);
