CREATE TABLE IF NOT EXISTS owner_accounts (
    id UUID PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash BYTEA,
    password_disabled_at TIMESTAMPTZ,
    recovery_public_key BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS owner_accounts_singleton_idx
    ON owner_accounts ((TRUE));

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
    key_agreement_public_key BYTEA,
    wrapped_vault_key BYTEA,
    approved_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS auth_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id UUID NOT NULL REFERENCES owner_accounts(id) ON DELETE CASCADE,
    access_token_hash BYTEA NOT NULL UNIQUE,
    refresh_token_hash BYTEA NOT NULL UNIQUE,
    access_expires_at TIMESTAMPTZ NOT NULL,
    refresh_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS auth_sessions_owner_active_idx
    ON auth_sessions(owner_id, refresh_expires_at)
    WHERE revoked_at IS NULL;

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
    numeric_id SMALLINT NOT NULL CHECK (numeric_id BETWEEN 1 AND 99),
    profile_ciphertext BYTEA NOT NULL,
    identity_public_key BYTEA NOT NULL,
    command_public_key BYTEA CHECK (
        command_public_key IS NULL OR octet_length(command_public_key) = 32
    ),
    wrapped_vault_key BYTEA,
    paired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ,
    radio_access_technology TEXT,
    signal_bars SMALLINT CHECK (signal_bars IS NULL OR signal_bars BETWEEN 0 AND 4),
    radio_status_updated_at TIMESTAMPTZ,
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
    owner_id UUID REFERENCES owner_accounts(id) ON DELETE CASCADE,
    paired_device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS pairing_tokens_owner_idx
    ON pairing_tokens(owner_id, created_at DESC);

CREATE TABLE IF NOT EXISTS device_credentials (
    device_id UUID PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
    credential_hash BYTEA NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS sim_cards (
    id UUID PRIMARY KEY,
    numeric_id SMALLINT NOT NULL CHECK (numeric_id BETWEEN 1 AND 99),
    fingerprint_scope UUID REFERENCES devices(id) ON DELETE CASCADE,
    iccid_fingerprint BYTEA CHECK (
        iccid_fingerprint IS NULL OR octet_length(iccid_fingerprint) = 32
    ),
    profile_ciphertext BYTEA NOT NULL DEFAULT ''::bytea,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    retired_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS sim_cards_active_numeric_id_idx
    ON sim_cards(numeric_id)
    WHERE retired_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS sim_cards_fingerprint_idx
    ON sim_cards(encode(iccid_fingerprint, 'hex'))
    WHERE iccid_fingerprint IS NOT NULL AND retired_at IS NULL;

CREATE TABLE IF NOT EXISTS device_slots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    slot_key TEXT NOT NULL,
    slot_index SMALLINT NOT NULL CHECK (slot_index > 0),
    current_sim_id UUID REFERENCES sim_cards(id) ON DELETE SET NULL,
    radio_access_technology TEXT,
    signal_bars SMALLINT CHECK (signal_bars IS NULL OR signal_bars BETWEEN 0 AND 4),
    radio_status_updated_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ,
    removed_at TIMESTAMPTZ,
    UNIQUE (device_id, slot_key),
    UNIQUE (device_id, slot_index)
);

CREATE INDEX IF NOT EXISTS device_slots_device_idx
    ON device_slots(device_id);

CREATE TABLE IF NOT EXISTS slot_sim_assignments (
    id BIGSERIAL PRIMARY KEY,
    slot_id UUID NOT NULL REFERENCES device_slots(id) ON DELETE CASCADE,
    sim_id UUID NOT NULL REFERENCES sim_cards(id) ON DELETE CASCADE,
    inserted_at TIMESTAMPTZ NOT NULL,
    removed_at TIMESTAMPTZ,
    CHECK (removed_at IS NULL OR removed_at >= inserted_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS slot_sim_assignments_current_idx
    ON slot_sim_assignments(slot_id)
    WHERE removed_at IS NULL;

CREATE INDEX IF NOT EXISTS slot_sim_assignments_sim_history_idx
    ON slot_sim_assignments(sim_id, inserted_at DESC);

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

CREATE INDEX IF NOT EXISTS events_device_sequence_idx
    ON events(device_id, sequence);

CREATE INDEX IF NOT EXISTS events_expiry_idx
    ON events(expires_at);

CREATE TABLE IF NOT EXISTS messages (
    event_id UUID PRIMARY KEY REFERENCES events(id) ON DELETE CASCADE,
    conversation_id UUID NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('incoming', 'outgoing')),
    media_kind TEXT NOT NULL CHECK (media_kind IN ('sms', 'mms')),
    delivery_state TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS messages_conversation_idx
    ON messages(conversation_id, updated_at DESC);

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
    expires_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT
);

CREATE INDEX IF NOT EXISTS outbox_available_idx
    ON outbox_jobs(available_at)
    WHERE completed_at IS NULL;

CREATE INDEX IF NOT EXISTS outbox_delivery_idx
    ON outbox_jobs(available_at, id)
    WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS audit_events (
    id BIGSERIAL PRIMARY KEY,
    actor_type TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    action TEXT NOT NULL,
    target_id TEXT,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS encrypted_device_commands (
    id UUID PRIMARY KEY,
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    sender_installation_id UUID NOT NULL REFERENCES app_installations(id),
    sender_public_key BYTEA NOT NULL CHECK (octet_length(sender_public_key) = 32),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext BYTEA NOT NULL CHECK (octet_length(ciphertext) BETWEEN 1 AND 65536),
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    leased_until TIMESTAMPTZ,
    delivery_attempts INTEGER NOT NULL DEFAULT 0,
    result_nonce BYTEA CHECK (result_nonce IS NULL OR octet_length(result_nonce) = 12),
    result_ciphertext BYTEA CHECK (
        result_ciphertext IS NULL OR octet_length(result_ciphertext) BETWEEN 1 AND 65536
    ),
    completed_at TIMESTAMPTZ,
    CHECK (expires_at > created_at),
    CHECK ((result_nonce IS NULL) = (result_ciphertext IS NULL)),
    CHECK ((completed_at IS NULL) = (result_ciphertext IS NULL))
);

CREATE INDEX IF NOT EXISTS encrypted_device_commands_delivery_idx
    ON encrypted_device_commands(device_id, created_at)
    WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS incoming_call_notifications (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type = 'call.incoming'),
    device_id UUID NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    occurred_at TIMESTAMPTZ NOT NULL,
    key_epoch INTEGER NOT NULL CHECK (key_epoch > 0),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS incoming_call_notifications_expiry_idx
    ON incoming_call_notifications(expires_at);

CREATE TABLE IF NOT EXISTS incoming_call_recipients (
    notification_id UUID NOT NULL REFERENCES incoming_call_notifications(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES app_installations(id) ON DELETE CASCADE,
    sender_public_key BYTEA NOT NULL CHECK (octet_length(sender_public_key) = 32),
    nonce BYTEA NOT NULL CHECK (octet_length(nonce) = 12),
    ciphertext BYTEA NOT NULL,
    signature BYTEA NOT NULL CHECK (octet_length(signature) = 64),
    PRIMARY KEY (notification_id, installation_id)
);

CREATE INDEX IF NOT EXISTS incoming_call_recipients_installation_idx
    ON incoming_call_recipients(installation_id, notification_id);

CREATE TABLE IF NOT EXISTS incoming_call_claims (
    notification_id UUID PRIMARY KEY REFERENCES incoming_call_notifications(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES app_installations(id),
    claimed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS incoming_call_claims_installation_idx
    ON incoming_call_claims(installation_id, claimed_at DESC);

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
    slot_key TEXT NOT NULL DEFAULT '1',
    slot_index SMALLINT NOT NULL DEFAULT 1 CHECK (slot_index > 0),
    sim_fingerprint TEXT CHECK (
        sim_fingerprint IS NULL OR sim_fingerprint ~ '^[0-9a-f]{64}$'
    ),
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
