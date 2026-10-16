DELETE FROM encrypted_device_commands;

ALTER TABLE encrypted_device_commands
    ADD COLUMN IF NOT EXISTS sender_installation_id UUID,
    ADD COLUMN IF NOT EXISTS signature BYTEA;

ALTER TABLE encrypted_device_commands
    ALTER COLUMN sender_installation_id SET NOT NULL,
    ALTER COLUMN signature SET NOT NULL;

ALTER TABLE encrypted_device_commands
    DROP CONSTRAINT IF EXISTS encrypted_device_commands_signature_length;

ALTER TABLE encrypted_device_commands
    ADD CONSTRAINT encrypted_device_commands_signature_length
    CHECK (octet_length(signature) = 64);

ALTER TABLE encrypted_device_commands
    DROP CONSTRAINT IF EXISTS encrypted_device_commands_sender_installation_fk;

ALTER TABLE encrypted_device_commands
    ADD CONSTRAINT encrypted_device_commands_sender_installation_fk
    FOREIGN KEY (sender_installation_id) REFERENCES app_installations(id);
