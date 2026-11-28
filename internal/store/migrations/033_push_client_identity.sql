-- Keep one notification-channel device ID when a browser rotates its push endpoint.
ALTER TABLE push_subscriptions ADD COLUMN client_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX push_subscriptions_client_id ON push_subscriptions(client_id) WHERE client_id <> '';
