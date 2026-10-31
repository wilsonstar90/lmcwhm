-- Dedicated rules: paranoia level (active when the WAF level is at least
-- this), action (block | detect) and the status a block answers with.
ALTER TABLE waf_rules ADD COLUMN level INTEGER NOT NULL DEFAULT 1;
ALTER TABLE waf_rules ADD COLUMN action TEXT NOT NULL DEFAULT 'block';
ALTER TABLE waf_rules ADD COLUMN status INTEGER NOT NULL DEFAULT 403;
UPDATE waf_rules SET level = 2 WHERE code IN ('wp-1004', 'wp-1005', 'wp-1006', 'wp-1010');
