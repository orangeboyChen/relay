CREATE TABLE IF NOT EXISTS device_discovery_sessions (
    owner_id UUID PRIMARY KEY REFERENCES owner_accounts(id) ON DELETE CASCADE,
    opened_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    closed_at TIMESTAMPTZ,
    CHECK (expires_at > opened_at)
);

CREATE TABLE IF NOT EXISTS device_discoveries (
    device_id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES owner_accounts(id) ON DELETE CASCADE,
    identity_public_key BYTEA NOT NULL CHECK (octet_length(identity_public_key) = 32),
    command_public_key BYTEA NOT NULL CHECK (octet_length(command_public_key) = 32),
    credential_hash BYTEA NOT NULL UNIQUE CHECK (octet_length(credential_hash) = 32),
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    first_seen_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    decided_at TIMESTAMPTZ,
    paired_device_id UUID REFERENCES devices(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS device_discoveries_owner_pending_idx
    ON device_discoveries(owner_id, first_seen_at)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS device_discoveries_expiry_idx
    ON device_discoveries(expires_at);
