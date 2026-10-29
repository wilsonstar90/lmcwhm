-- Port forwarding (端口轉發): TCP / UDP from a listen port to a local port or
-- another IPv4 host, in its own nftables table.
CREATE TABLE port_forwards (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    proto       TEXT NOT NULL,              -- tcp | udp | both
    listen      INTEGER NOT NULL,
    target_host TEXT NOT NULL DEFAULT '',   -- '' = this server
    target_port INTEGER NOT NULL,
    source      TEXT NOT NULL DEFAULT '',   -- allowed source (IP / CIDR), '' = any
    remark      TEXT NOT NULL DEFAULT '',
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  INTEGER NOT NULL
);
