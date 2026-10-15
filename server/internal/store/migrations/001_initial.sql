CREATE TABLE IF NOT EXISTS owner_accounts (
    id UUID PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash BYTEA,
    password_disabled_at TIMESTAMPTZ,
    recovery_public_key BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id BYTEA PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES owner_accounts(id) ON DELETE CASCADE,
    public_key BYTEA NOT NULL,
    sign_count BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS app_installations (
    id UUID PRIMARY KEY,
    owner_id UUID NOT NULL REFERENCES owner_accounts(id) ON DELETE CASCADE,
    identity_public_key BYTEA NOT NULL,
    wrapped_vault_key BYTEA,
    approved_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS push_tokens (
    installation_id UUID NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
    environment TEXT NOT NULL CHECK (environment IN ('sandbox', 'production')),
    kind TEXT NOT NULL CHECK (kind IN ('alert', 'voip')),
    token_ciphertext BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (installation_id, environment, kind)
);

CREATE TABLE IF NOT EXISTS devices (
    id UUID PRIMARY KEY,
    numeric_id SMALLINT NOT NULL UNIQUE CHECK (numeric_id BETWEEN 1 AND 99),
    profile_ciphertext BYTEA NOT NULL,
    identity_public_key BYTEA NOT NULL,
    wrapped_vault_key BYTEA,
    paired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS device_certificates (
    serial TEXT PRIMARY KEY,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    certificate_pem BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS pairing_tokens (
    token_hash BYTEA PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS events (
    sequence BIGSERIAL PRIMARY KEY,
    id UUID NOT NULL UNIQUE,
    type TEXT NOT NULL,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    occurred_at TIMESTAMPTZ NOT NULL,
    key_epoch INTEGER NOT NULL CHECK (key_epoch > 0),
    nonce BYTEA NOT NULL,
    ciphertext BYTEA NOT NULL,
    signature BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS events_device_sequence_idx ON events(device_id, sequence);
CREATE INDEX IF NOT EXISTS events_expiry_idx ON events(expires_at);

CREATE TABLE IF NOT EXISTS messages (
    event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    conversation_id UUID NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('incoming', 'outgoing')),
    media_kind TEXT NOT NULL CHECK (media_kind IN ('sms', 'mms')),
    delivery_state TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS messages_conversation_idx ON messages(conversation_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS attachments (
    id UUID PRIMARY KEY,
    message_event_id UUID NOT NULL REFERENCES messages(event_id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL UNIQUE,
    encrypted_size BIGINT NOT NULL CHECK (encrypted_size >= 0),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS calls (
    event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    call_id UUID NOT NULL UNIQUE,
    direction TEXT NOT NULL CHECK (direction IN ('incoming', 'outgoing')),
    state TEXT NOT NULL,
    claimed_by UUID REFERENCES app_installations(id),
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS outbox_jobs (
    id BIGSERIAL PRIMARY KEY,
    kind TEXT NOT NULL,
    routing_key TEXT NOT NULL,
    payload BYTEA NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    last_error TEXT
);

CREATE INDEX IF NOT EXISTS outbox_available_idx ON outbox_jobs(available_at) WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target_id TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
