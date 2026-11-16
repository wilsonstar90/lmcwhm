-- Unified action metadata and durable running state for panel tasks.
ALTER TABLE task_history ADD COLUMN action TEXT NOT NULL DEFAULT '';
ALTER TABLE task_history ADD COLUMN resource TEXT NOT NULL DEFAULT '';
ALTER TABLE task_history ADD COLUMN idempotency_key TEXT NOT NULL DEFAULT '';

CREATE INDEX task_history_status_started ON task_history(status, started);
CREATE INDEX task_history_idempotency ON task_history(idempotency_key);
