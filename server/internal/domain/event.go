package domain

import (
	"errors"
	"time"
)

// HistorySnapshot is one owner-scoped encrypted archive.
type HistorySnapshot struct {
	OwnerID    string
	Kind       int
	Revision   int64
	Ciphertext []byte
	UpdatedAt  time.Time
}

var ErrIdempotencyConflict = errors.New("event id was already used with different content")

// EventEnvelope carries encrypted and signed application data through the server.
type EventEnvelope struct {
	ID                      string    `json:"id"`
	Sequence                int64     `json:"sequence"`
	Type                    string    `json:"type"`
	DeviceUUID              string    `json:"device_uuid"`
	RecipientInstallationID string    `json:"recipient_installation_id,omitempty"`
	OccurredAt              time.Time `json:"occurred_at"`
	KeyEpoch                int       `json:"key_epoch"`
	Nonce                   []byte    `json:"nonce"`
	Ciphertext              []byte    `json:"ciphertext"`
	Signature               []byte    `json:"signature"`
}
