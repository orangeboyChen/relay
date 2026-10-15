CREATE TABLE IF NOT EXISTS incoming_call_notifications (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type = 'call.incoming'),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    occurred_at TIMESTAMPTZ NOT NULL,
    key_epoch INTEGER NOT NULL CHECK (key_epoch > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS incoming_call_recipients (
    notification_id UUID NOT NULL REFERENCES incoming_call_notifications(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
    sender_public_key BYTEA NOT NULL CHECK (octet_length(sender_public_key) = 32),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext BYTEA NOT NULL,
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    PRIMARY KEY (notification_id, installation_id)
);

CREATE INDEX IF NOT EXISTS incoming_call_notifications_expiry_idx
    ON incoming_call_notifications(expires_at);

CREATE INDEX IF NOT EXISTS incoming_call_recipients_installation_idx
    ON incoming_call_recipients(installation_id, notification_id);
