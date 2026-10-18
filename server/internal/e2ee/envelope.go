package e2ee

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"

	"sim-deck-server/internal/domain"
)

var (
	ErrInvalidEnvelope   = errors.New("event envelope is invalid")
	ErrInvalidVaultKey   = errors.New("vault key must contain exactly 32 bytes")
	ErrInvalidSigningKey = errors.New("event signing key is invalid")
	ErrInvalidSignature  = errors.New("event envelope signature is invalid")
)

const (
	MaxEventIDBytes        = 128
	MaxEventTypeBytes      = 128
	MaxDeviceUUIDBytes     = 128
	MaxCiphertextBytes     = 4 << 20
	gcmNonceBytes          = 12
	gcmAuthenticationBytes = 16
)

// Seal encrypts a payload and signs both the routing metadata and ciphertext.
func Seal(key []byte, signingKey ed25519.PrivateKey, envelope domain.EventEnvelope, plaintext []byte) (domain.EventEnvelope, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return domain.EventEnvelope{}, err
	}
	if len(signingKey) != ed25519.PrivateKeySize {
		return domain.EventEnvelope{}, ErrInvalidSigningKey
	}
	if err := validateRoutingMetadata(envelope); err != nil {
		return domain.EventEnvelope{}, err
	}
	if len(plaintext) > MaxCiphertextBytes-aead.Overhead() {
		return domain.EventEnvelope{}, fmt.Errorf("%w: plaintext exceeds %d bytes", ErrInvalidEnvelope, MaxCiphertextBytes-aead.Overhead())
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
	if err := VerifyEnvelope(verificationKey, envelope); err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	if err != nil {
		return nil, fmt.Errorf("decrypt event envelope: %w", err)
	}
	return plaintext, nil
}

// VerifyEnvelope validates an envelope and authenticates its metadata and ciphertext without decrypting it.
func VerifyEnvelope(verificationKey ed25519.PublicKey, envelope domain.EventEnvelope) error {
	if len(verificationKey) != ed25519.PublicKeySize {
		return ErrInvalidSigningKey
	}
	if err := validateEnvelope(envelope); err != nil {
		return err
	}
	if !ed25519.Verify(verificationKey, signedData(envelope), envelope.Signature) {
		return ErrInvalidSignature
	}
	return nil
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
	buffer = appendString(buffer, envelope.Type)
	buffer = appendString(buffer, envelope.DeviceUUID)
	if envelope.RecipientInstallationID != "" {
		buffer = appendString(buffer, envelope.RecipientInstallationID)
	}
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.KeyEpoch))
	return buffer
}

func validateEnvelope(envelope domain.EventEnvelope) error {
	if err := validateRoutingMetadata(envelope); err != nil {
		return err
	}
	if len(envelope.Nonce) != gcmNonceBytes {
		return fmt.Errorf("%w: nonce must contain exactly %d bytes", ErrInvalidEnvelope, gcmNonceBytes)
	}
	if len(envelope.Ciphertext) < gcmAuthenticationBytes {
		return fmt.Errorf("%w: ciphertext is too short", ErrInvalidEnvelope)
	}
	if len(envelope.Ciphertext) > MaxCiphertextBytes {
		return fmt.Errorf("%w: ciphertext exceeds %d bytes", ErrInvalidEnvelope, MaxCiphertextBytes)
	}
	if len(envelope.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature must contain exactly %d bytes", ErrInvalidEnvelope, ed25519.SignatureSize)
	}
	return nil
}

func validateRoutingMetadata(envelope domain.EventEnvelope) error {
	if err := validateBoundedValue("id", envelope.ID, MaxEventIDBytes); err != nil {
		return err
	}
	if envelope.Sequence < 0 {
		return fmt.Errorf("%w: sequence cannot be negative", ErrInvalidEnvelope)
	}
	if err := validateBoundedValue("type", envelope.Type, MaxEventTypeBytes); err != nil {
		return err
	}
	if err := validateBoundedValue("device UUID", envelope.DeviceUUID, MaxDeviceUUIDBytes); err != nil {
		return err
	}
	if envelope.RecipientInstallationID != "" {
		if err := validateBoundedValue("recipient installation ID", envelope.RecipientInstallationID, MaxDeviceUUIDBytes); err != nil {
			return err
		}
	}
	if envelope.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at is required", ErrInvalidEnvelope)
	}
	if envelope.KeyEpoch < 1 {
		return fmt.Errorf("%w: key epoch must be positive", ErrInvalidEnvelope)
	}
	return nil
}

func validateBoundedValue(name string, value string, maximum int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidEnvelope, name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalidEnvelope, name, maximum)
	}
	return nil
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
