CREATE TABLE sites (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    domain         TEXT NOT NULL UNIQUE,
    aliases        TEXT NOT NULL DEFAULT '[]',
    type           TEXT NOT NULL,              -- php | static
    php_version    TEXT NOT NULL DEFAULT '',
    root           TEXT NOT NULL,
    stopped        INTEGER NOT NULL DEFAULT 0,
    force_https    INTEGER NOT NULL DEFAULT 0,
    hsts           INTEGER NOT NULL DEFAULT 0,
    ssl_mode       TEXT NOT NULL DEFAULT 'none', -- none | acme | custom
    ssl_challenge  TEXT NOT NULL DEFAULT 'http', -- http | dns
    ssl_wildcard   INTEGER NOT NULL DEFAULT 0,
    cert_issuer    TEXT NOT NULL DEFAULT '',
    cert_not_after INTEGER NOT NULL DEFAULT 0,
    cert_error     TEXT NOT NULL DEFAULT '',
    dns_managed    INTEGER NOT NULL DEFAULT 0,
    dns_proxied    INTEGER NOT NULL DEFAULT 0,
    remark         TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL
);
