-- Portable checksum metadata for newly created backup archives.
ALTER TABLE backups ADD COLUMN sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN manifest_location TEXT NOT NULL DEFAULT '';
