ALTER TABLE outbox_jobs
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

UPDATE outbox_jobs
SET expires_at = available_at + INTERVAL '2 minutes'
WHERE expires_at IS NULL
  AND kind LIKE 'voip.%';

CREATE INDEX IF NOT EXISTS outbox_delivery_idx
    ON outbox_jobs(available_at, id)
    WHERE completed_at IS NULL;
