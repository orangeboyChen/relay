package domain

import "time"

// EncryptedCommand is an opaque end-to-end encrypted instruction routed to one device.
type EncryptedCommand struct {
	ID              string    `json:"id"`
	DeviceUUID      string    `json:"device_uuid"`
	SenderPublicKey []byte    `json:"sender_public_key"`
	Nonce           []byte    `json:"nonce"`
	Ciphertext      []byte    `json:"ciphertext"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

// EncryptedCommandResult is an opaque response encrypted by the target device.
type EncryptedCommandResult struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// CommandStatus exposes delivery state without revealing command or result plaintext.
type CommandStatus struct {
	ID          string                  `json:"id"`
	DeviceUUID  string                  `json:"device_uuid"`
	State       string                  `json:"state"`
	Result      *EncryptedCommandResult `json:"result,omitempty"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}
