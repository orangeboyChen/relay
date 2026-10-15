package domain

import "time"

// IncomingCallRecipient carries one iPhone-specific opaque call payload.
type IncomingCallRecipient struct {
	InstallationID  string `json:"installation_id"`
	SenderPublicKey []byte `json:"sender_public_key"`
	Nonce           []byte `json:"nonce"`
	Ciphertext      []byte `json:"ciphertext"`
	Signature       []byte `json:"signature"`
}

// IncomingCallNotification contains signed routing data and no call plaintext.
type IncomingCallNotification struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	DeviceUUID string                  `json:"device_uuid"`
	OccurredAt time.Time               `json:"occurred_at"`
	KeyEpoch   int                     `json:"key_epoch"`
	Recipients []IncomingCallRecipient `json:"recipients"`
}
