-- Ownership and module metadata for durable, cancellable tasks.
ALTER TABLE task_history ADD COLUMN owner TEXT NOT NULL DEFAULT '';
ALTER TABLE task_history ADD COLUMN module TEXT NOT NULL DEFAULT '';
