-- Database settings window: keep the service last in line for the OOM killer.
ALTER TABLE apps ADD COLUMN oom_protect INTEGER NOT NULL DEFAULT 0;
