package e2ee

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"sim-deck-server/internal/domain"
)

var (
	ErrInvalidVaultKey  = errors.New("vault key must contain exactly 32 bytes")
	ErrInvalidSignature = errors.New("event envelope signature is invalid")
)

// Seal encrypts a payload and signs both the routing metadata and ciphertext.
func Seal(key []byte, signingKey ed25519.PrivateKey, envelope domain.EventEnvelope, plaintext []byte) (domain.EventEnvelope, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return domain.EventEnvelope{}, err
	}
	if len(signingKey) != ed25519.PrivateKeySize {
		return domain.EventEnvelope{}, fmt.Errorf("invalid signing key")
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return domain.EventEnvelope{}, fmt.Errorf("generate nonce: %w", err)
	}

	envelope.Nonce = nonce
	envelope.Ciphertext = aead.Seal(nil, nonce, plaintext, associatedData(envelope))
	envelope.Signature = ed25519.Sign(signingKey, signedData(envelope))
	return envelope, nil
}

// Open verifies an envelope before decrypting its content.
func Open(key []byte, verificationKey ed25519.PublicKey, envelope domain.EventEnvelope) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	if len(verificationKey) != ed25519.PublicKeySize || !ed25519.Verify(verificationKey, signedData(envelope), envelope.Signature) {
		return nil, ErrInvalidSignature
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	if err != nil {
		return nil, fmt.Errorf("decrypt event envelope: %w", err)
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrInvalidVaultKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func associatedData(envelope domain.EventEnvelope) []byte {
	buffer := make([]byte, 0, 128)
	buffer = appendString(buffer, envelope.ID)
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.Sequence))
	buffer = appendString(buffer, envelope.Type)
	buffer = appendString(buffer, envelope.DeviceUUID)
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.KeyEpoch))
	return buffer
}

func signedData(envelope domain.EventEnvelope) []byte {
	buffer := associatedData(envelope)
	buffer = appendBytes(buffer, envelope.Nonce)
	buffer = appendBytes(buffer, envelope.Ciphertext)
	return buffer
}

func appendString(buffer []byte, value string) []byte {
	return appendBytes(buffer, []byte(value))
}

func appendBytes(buffer []byte, value []byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...)
}

// NewEnvelope creates routing metadata without adding plaintext content.
func NewEnvelope(id string, sequence int64, eventType string, deviceUUID string, occurredAt time.Time, keyEpoch int) domain.EventEnvelope {
	return domain.EventEnvelope{
		ID:         id,
		Sequence:   sequence,
		Type:       eventType,
		DeviceUUID: deviceUUID,
		OccurredAt: occurredAt.UTC(),
		KeyEpoch:   keyEpoch,
	}
}
