-- Remote access per database (MySQL / MariaDB): local only, everyone, or
-- listed addresses. rule_id is the firewall port rule the panel added for it
-- (0 = none); rule_owned = the panel created it (and removes it when done),
-- 0 = an existing rule already covered the addresses.
CREATE TABLE db_access (
    db_id   INTEGER PRIMARY KEY REFERENCES databases(id) ON DELETE CASCADE,
    mode    TEXT NOT NULL DEFAULT 'local',
    ips     TEXT NOT NULL DEFAULT '',
    rule_id INTEGER NOT NULL DEFAULT 0,
    rule_owned INTEGER NOT NULL DEFAULT 0
);

-- A database engine publishes its port on every address (not only loopback)
-- while one of its databases allows remote access.
ALTER TABLE apps ADD COLUMN public INTEGER NOT NULL DEFAULT 0;
