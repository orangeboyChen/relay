ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS command_public_key BYTEA
    CHECK (command_public_key IS NULL OR octet_length(command_public_key) = 32);

CREATE TABLE IF NOT EXISTS encrypted_device_commands (
    id UUID PRIMARY KEY,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    sender_public_key BYTEA NOT NULL CHECK (octet_length(sender_public_key) = 32),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 65536),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    leased_until TIMESTAMPTZ,
    delivery_attempts INTEGER NOT NULL DEFAULT 0,
    result_nonce BYTEA CHECK (result_nonce IS NULL OR octet_length(result_nonce) = 12),
    result_ciphertext BYTEA CHECK (result_ciphertext IS NULL OR octet_length(result_ciphertext) BETWEEN 1 AND 65536),
    completed_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK ((result_nonce IS NULL) = (result_ciphertext IS NULL)),
    CHECK ((completed_at IS NULL) = (result_ciphertext IS NULL))
);

CREATE INDEX IF NOT EXISTS encrypted_device_commands_delivery_idx
    ON encrypted_device_commands(device_id, created_at)
    WHERE completed_at IS NULL;
