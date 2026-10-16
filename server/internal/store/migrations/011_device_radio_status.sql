ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS radio_access_technology TEXT,
    ADD COLUMN IF NOT EXISTS signal_bars SMALLINT,
    ADD COLUMN IF NOT EXISTS radio_status_updated_at TIMESTAMPTZ;

ALTER TABLE devices
    DROP CONSTRAINT IF EXISTS devices_signal_bars_check;

ALTER TABLE devices
    ADD CONSTRAINT devices_signal_bars_check
    CHECK (signal_bars IS NULL OR signal_bars BETWEEN 0 AND 4);
