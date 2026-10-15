package incomingcall

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
)

const (
	contextLabel    = "sim-deck-incoming-call-v1"
	eventType       = "call.incoming"
	protocolVersion = 1
	keyEpoch        = 1
	keyBytes        = 32
	nonceBytes      = 12
)

var phoneNumberPattern = regexp.MustCompile(`^\+?[0-9]{1,20}$`)

// Recipient is one approved iPhone public encryption identity.
type Recipient struct {
	InstallationID        string `json:"installation_id"`
	KeyAgreementPublicKey []byte `json:"key_agreement_public_key"`
}

// RecipientEnvelope is one independently encrypted incoming-call payload.
type RecipientEnvelope struct {
	InstallationID  string `json:"installation_id"`
	SenderPublicKey []byte `json:"sender_public_key"`
	Nonce           []byte `json:"nonce"`
	Ciphertext      []byte `json:"ciphertext"`
	Signature       []byte `json:"signature"`
}

// Notification carries only signed routing metadata and recipient ciphertexts.
type Notification struct {
	ID         string              `json:"id"`
	Type       string              `json:"type"`
	DeviceUUID string              `json:"device_uuid"`
	OccurredAt time.Time           `json:"occurred_at"`
	KeyEpoch   int                 `json:"key_epoch"`
	Recipients []RecipientEnvelope `json:"recipients"`
}

// Payload is visible only to an approved iPhone installation.
type Payload struct {
	Version        int        `json:"version"`
	CallID         string     `json:"call_id"`
	DeviceUUID     string     `json:"device_uuid"`
	NumericID      int        `json:"numeric_id"`
	RemoteNumber   string     `json:"remote_number"`
	State          call.State `json:"state"`
	CallSecret     []byte     `json:"call_secret"`
	NotificationID string     `json:"notification_id"`
}

// CallContext is the call-scoped identity and secret used for future commands and events.
type CallContext struct {
	CallID       string
	SharedSecret []byte
}

// Builder seals incoming-call metadata for every approved iPhone.
type Builder struct {
	identity identity.State
	random   io.Reader
	now      func() time.Time
}

// NewBuilder creates an incoming-call notification builder for one bound modem.
func NewBuilder(state identity.State) (*Builder, error) {
	if state.DeviceUUID == "" || state.NumericID < 1 || state.NumericID > 99 || len(state.IdentityPrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("incoming-call builder requires a valid bound device identity")
	}
	return &Builder{identity: state, random: rand.Reader, now: time.Now}, nil
}

// Seal creates a signed fan-out notification without exposing call plaintext.
func (builder *Builder) Seal(recipients []Recipient, snapshot call.Snapshot) (Notification, CallContext, error) {
	validated, err := validateRecipients(recipients)
	if err != nil {
		return Notification{}, CallContext{}, err
	}
	if (snapshot.State != call.StateRinging && snapshot.State != call.StateWaiting) || !phoneNumberPattern.MatchString(snapshot.IncomingNumber) {
		return Notification{}, CallContext{}, errors.New("ringing call with a valid incoming number is required")
	}
	callID, err := randomUUID(builder.random)
	if err != nil {
		return Notification{}, CallContext{}, fmt.Errorf("generate incoming call id: %w", err)
	}
	notificationID, err := randomUUID(builder.random)
	if err != nil {
		return Notification{}, CallContext{}, fmt.Errorf("generate incoming notification id: %w", err)
	}
	callSecret := make([]byte, keyBytes)
	if _, err := io.ReadFull(builder.random, callSecret); err != nil {
		return Notification{}, CallContext{}, fmt.Errorf("generate incoming call secret: %w", err)
	}
	payload, err := json.Marshal(Payload{
		Version: protocolVersion, CallID: callID, DeviceUUID: builder.identity.DeviceUUID,
		NumericID: builder.identity.NumericID, RemoteNumber: snapshot.IncomingNumber,
		State: snapshot.State, CallSecret: callSecret, NotificationID: notificationID,
	})
	if err != nil {
		return Notification{}, CallContext{}, fmt.Errorf("encode incoming call payload: %w", err)
	}
	notification := Notification{
		ID: notificationID, Type: eventType, DeviceUUID: builder.identity.DeviceUUID,
		OccurredAt: builder.now().UTC().Truncate(time.Millisecond), KeyEpoch: keyEpoch,
		Recipients: make([]RecipientEnvelope, 0, len(validated)),
	}
	for _, recipient := range validated {
		sealed, err := builder.sealRecipient(notification, recipient, payload)
		if err != nil {
			return Notification{}, CallContext{}, err
		}
		notification.Recipients = append(notification.Recipients, sealed)
	}
	return notification, CallContext{CallID: callID, SharedSecret: append([]byte(nil), callSecret...)}, nil
}

func (builder *Builder) sealRecipient(notification Notification, recipient Recipient, plaintext []byte) (RecipientEnvelope, error) {
	privateKey, err := ecdh.X25519().GenerateKey(builder.random)
	if err != nil {
		return RecipientEnvelope{}, fmt.Errorf("generate recipient ephemeral key: %w", err)
	}
	publicKey, err := ecdh.X25519().NewPublicKey(recipient.KeyAgreementPublicKey)
	if err != nil {
		return RecipientEnvelope{}, fmt.Errorf("parse recipient agreement key: %w", err)
	}
	sharedSecret, err := privateKey.ECDH(publicKey)
	if err != nil {
		return RecipientEnvelope{}, fmt.Errorf("derive recipient agreement key: %w", err)
	}
	aead, err := recipientAEAD(sharedSecret, notification.ID, recipient.InstallationID)
	if err != nil {
		return RecipientEnvelope{}, err
	}
	nonce := make([]byte, nonceBytes)
	if _, err := io.ReadFull(builder.random, nonce); err != nil {
		return RecipientEnvelope{}, fmt.Errorf("generate recipient nonce: %w", err)
	}
	envelope := RecipientEnvelope{
		InstallationID:  recipient.InstallationID,
		SenderPublicKey: append([]byte(nil), privateKey.PublicKey().Bytes()...),
		Nonce:           nonce,
	}
	envelope.Ciphertext = aead.Seal(nil, nonce, plaintext, recipientAssociatedData(notification, envelope))
	envelope.Signature = ed25519.Sign(builder.identity.IdentityPrivateKey, recipientSignedData(notification, envelope))
	return envelope, nil
}

func validateRecipients(recipients []Recipient) ([]Recipient, error) {
	if len(recipients) == 0 {
		return nil, errors.New("at least one approved installation recipient is required")
	}
	validated := append([]Recipient(nil), recipients...)
	sort.Slice(validated, func(left int, right int) bool {
		return validated[left].InstallationID < validated[right].InstallationID
	})
	for index, recipient := range validated {
		if !validUUID(recipient.InstallationID) || len(recipient.KeyAgreementPublicKey) != keyBytes {
			return nil, errors.New("installation recipient identity is invalid")
		}
		if index > 0 && recipient.InstallationID == validated[index-1].InstallationID {
			return nil, errors.New("installation recipient identity is duplicated")
		}
	}
	return validated, nil
}

func recipientAEAD(sharedSecret []byte, notificationID string, installationID string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, sharedSecret, []byte(notificationID+"\x00"+installationID), contextLabel, keyBytes)
	if err != nil {
		return nil, fmt.Errorf("derive recipient encryption key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create recipient cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

func recipientAssociatedData(notification Notification, recipient RecipientEnvelope) []byte {
	buffer := make([]byte, 0, 192)
	buffer = appendBytes(buffer, []byte(contextLabel))
	buffer = appendBytes(buffer, []byte(notification.ID))
	buffer = appendBytes(buffer, []byte(notification.Type))
	buffer = appendBytes(buffer, []byte(notification.DeviceUUID))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.KeyEpoch))
	buffer = appendBytes(buffer, []byte(recipient.InstallationID))
	return appendBytes(buffer, recipient.SenderPublicKey)
}

func recipientSignedData(notification Notification, recipient RecipientEnvelope) []byte {
	buffer := make([]byte, 0, 256)
	buffer = appendBytes(buffer, []byte(notification.ID))
	buffer = appendBytes(buffer, []byte(notification.Type))
	buffer = appendBytes(buffer, []byte(notification.DeviceUUID))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(notification.KeyEpoch))
	buffer = appendBytes(buffer, []byte(recipient.InstallationID))
	buffer = appendBytes(buffer, recipient.SenderPublicKey)
	buffer = appendBytes(buffer, recipient.Nonce)
	buffer = appendBytes(buffer, recipient.Ciphertext)
	return buffer
}

func appendBytes(buffer []byte, value []byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...)
}

func randomUUID(random io.Reader) (string, error) {
	value := make([]byte, 16)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}

func validUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}
