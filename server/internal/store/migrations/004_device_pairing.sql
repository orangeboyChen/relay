ALTER TABLE pairing_tokens
    ADD COLUMN IF NOT EXISTS owner_id UUID REFERENCES owner_accounts(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS pairing_tokens_owner_idx ON pairing_tokens(owner_id, created_at DESC);

CREATE TABLE IF NOT EXISTS device_credentials (
    device_id UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    credential_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);
