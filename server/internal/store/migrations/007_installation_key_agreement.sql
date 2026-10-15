ALTER TABLE app_installations
    ADD COLUMN IF NOT EXISTS key_agreement_public_key BYTEA;
