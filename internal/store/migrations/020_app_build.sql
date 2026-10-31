-- How an App Store version was installed: "fast" (the official pre-built
-- image) or "source" (compiled on this server), with the extra configure
-- options and compiler flags of a source build.
ALTER TABLE apps ADD COLUMN build_mode TEXT NOT NULL DEFAULT 'fast';
ALTER TABLE apps ADD COLUMN build_args TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN build_cflags TEXT NOT NULL DEFAULT '';
