-- Device-scoped Web Push subscriptions survive browser session logout.
CREATE TABLE push_settings (
    id          INTEGER PRIMARY KEY CHECK(id = 1),
    public_key  TEXT NOT NULL,
    private_key TEXT NOT NULL
);

CREATE TABLE push_subscriptions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL,
    endpoint_hash TEXT NOT NULL UNIQUE,
    subscription TEXT NOT NULL,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL,
    last_used   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX push_subscriptions_enabled ON push_subscriptions(enabled, id);
