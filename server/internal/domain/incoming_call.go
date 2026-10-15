package domain

import (
	"errors"
	"time"
)

var ErrUntrustedInstallationRecipient = errors.New("incoming call recipient is not an approved installation")

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

// IncomingCallDelivery is the single-recipient payload queued for PushKit.
type IncomingCallDelivery struct {
	NotificationID string                `json:"notification_id"`
	Type           string                `json:"type"`
	DeviceUUID     string                `json:"device_uuid"`
	OccurredAt     time.Time             `json:"occurred_at"`
	KeyEpoch       int                   `json:"key_epoch"`
	Recipient      IncomingCallRecipient `json:"recipient"`
}
