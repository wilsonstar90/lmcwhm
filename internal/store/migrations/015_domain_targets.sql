-- Domain entries (域名 / SNI): every site row is one entry (domain +
-- aliases, SSL, DNS) whose target can change: its own website files, another
-- website, a Docker container, a URL, TLS passthrough or a redirect.
ALTER TABLE sites ADD COLUMN target TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN target_site INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN container TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN redirect TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN redirect_code INTEGER NOT NULL DEFAULT 301;
ALTER TABLE sites ADD COLUMN redirect_path INTEGER NOT NULL DEFAULT 1;
UPDATE sites SET
    target = CASE
        WHEN type = 'proxy' AND passthrough = 1 THEN 'passthrough'
        WHEN type = 'proxy' AND remark LIKE 'docker: %' THEN 'docker'
        WHEN type = 'proxy' THEN 'url'
        ELSE 'site' END,
    container = CASE WHEN type = 'proxy' AND passthrough = 0 AND remark LIKE 'docker: %' THEN substr(remark, 9) ELSE '' END;
