package domain

import "time"

// EventEnvelope carries encrypted and signed application data through the server.
type EventEnvelope struct {
	ID         string    `json:"id"`
	Sequence   int64     `json:"sequence"`
	Type       string    `json:"type"`
	DeviceUUID string    `json:"device_uuid"`
	OccurredAt time.Time `json:"occurred_at"`
	KeyEpoch   int       `json:"key_epoch"`
	Nonce      []byte    `json:"nonce"`
	Ciphertext []byte    `json:"ciphertext"`
	Signature  []byte    `json:"signature"`
}
