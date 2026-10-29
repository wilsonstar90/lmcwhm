-- System port rules (SSH, panel, web) replace the "always allow" switch:
-- they are ordinary rules the user can see, disable or delete.
ALTER TABLE fw_rules ADD COLUMN system TEXT NOT NULL DEFAULT '';
