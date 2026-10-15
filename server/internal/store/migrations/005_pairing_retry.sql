ALTER TABLE pairing_tokens
    ADD COLUMN IF NOT EXISTS paired_device_id UUID REFERENCES devices(id) ON DELETE SET NULL;
