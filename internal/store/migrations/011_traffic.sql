-- Security overview: requests stopped by the WAF, rate limits and IP rules
-- (status 403 / 429 / 444) per hour.
ALTER TABLE site_stats_hourly ADD COLUMN blocked INTEGER NOT NULL DEFAULT 0;
