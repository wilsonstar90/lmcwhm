-- Requests the security layers refused or flagged (edge and WAF), read in
-- from the access logs and the ModSecurity audit log as they grow and kept
-- for 30 days, so the block log can show any day (the logs themselves only
-- keep what fits) and repeated offenders can be banned automatically.
CREATE TABLE sec_events (
    id       INTEGER PRIMARY KEY,
    time     INTEGER NOT NULL,
    ip       TEXT NOT NULL,
    host     TEXT NOT NULL DEFAULT '',
    site     TEXT NOT NULL DEFAULT '',
    method   TEXT NOT NULL DEFAULT '',
    uri      TEXT NOT NULL DEFAULT '',
    ua       TEXT NOT NULL DEFAULT '',
    status   INTEGER NOT NULL DEFAULT 0,
    action   TEXT NOT NULL,            -- blocked | detected | limited
    source   TEXT NOT NULL,            -- waf | rule | list | ip | protection | cc
    rule     TEXT NOT NULL DEFAULT '',
    message  TEXT NOT NULL DEFAULT '',
    severity TEXT NOT NULL DEFAULT '',
    messages TEXT NOT NULL DEFAULT ''  -- WAF: every matched rule (JSON)
);
CREATE INDEX sec_events_time ON sec_events(time);
CREATE INDEX sec_events_ip ON sec_events(ip, time);

-- How far each log has been read (inode: a rotated file starts over).
CREATE TABLE sec_events_cursor (
    path   TEXT PRIMARY KEY,
    inode  INTEGER NOT NULL,
    offset INTEGER NOT NULL
);
