CREATE TABLE IF NOT EXISTS incoming_call_claims (
    notification_id UUID PRIMARY KEY REFERENCES incoming_call_notifications(id) ON DELETE CASCADE,
    installation_id UUID NOT NULL REFERENCES app_installations(id),
    claimed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS incoming_call_claims_installation_idx
    ON incoming_call_claims(installation_id, claimed_at DESC);
