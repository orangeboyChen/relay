package messageevent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"sim-deck-device/internal/callevent"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/sms"
)

const (
	eventType       = "message.received"
	protocolVersion = 1
	keyEpoch        = 1
	keyBytes        = 32
	nonceBytes      = 12
	eventContext    = "sim-deck-message-event-v1"
)

var (
	phoneNumberPattern = regexp.MustCompile(`^\+?[0-9]{1,20}$`)
	messageIDPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)

	// ErrNoRecipients leaves the SMS pending until an iPhone is approved.
	ErrNoRecipients = errors.New("no approved installation can receive the SMS")
)

// Payload is visible only to the intended approved iPhone installation.
type Payload struct {
	Version        int    `json:"version"`
	MessageID      string `json:"message_id"`
	DeviceUUID     string `json:"device_uuid"`
	SlotID         string `json:"slot_id,omitempty"`
	SIMID          string `json:"sim_id,omitempty"`
	NumericID      int    `json:"numeric_id"`
	RemoteNumber   string `json:"remote_number"`
	Body           string `json:"body"`
	ReceivedAt     string `json:"received_at"`
	Segments       int    `json:"segments"`
	InstallationID string `json:"installation_id"`
}

// Remote provides approved recipient identities and opaque event upload.
type Remote interface {
	ListInstallationRecipients(context.Context, string, string) ([]incomingcall.Recipient, error)
	UploadEvent(context.Context, string, string, callevent.Envelope) error
}

// Publisher encrypts every incoming SMS independently for each approved iPhone.
type Publisher struct {
	remote   Remote
	identity identity.State
}

// NewPublisher creates an incoming message publisher for one bound modem.
func NewPublisher(remote Remote, state identity.State) (*Publisher, error) {
	if remote == nil || state.Credential == "" || state.NumericID < 1 || state.NumericID > 99 ||
		len(state.CommandPrivateKey) != keyBytes || len(state.IdentityPrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("message event publisher requires a bound device identity and remote")
	}
	return &Publisher{remote: remote, identity: state}, nil
}

// Publish seals and uploads a deterministic idempotent event for every approved installation.
func (publisher *Publisher) Publish(ctx context.Context, message sms.ReceivedMessage) error {
	if err := validateMessage(message); err != nil {
		return err
	}
	recipients, err := publisher.remote.ListInstallationRecipients(ctx, publisher.identity.DeviceUUID, publisher.identity.Credential)
	if err != nil {
		return fmt.Errorf("list message recipients: %w", err)
	}
	if len(recipients) == 0 {
		return ErrNoRecipients
	}
	sort.Slice(recipients, func(left int, right int) bool {
		return recipients[left].InstallationID < recipients[right].InstallationID
	})
	for index, recipient := range recipients {
		if index > 0 && recipient.InstallationID == recipients[index-1].InstallationID {
			return errors.New("message recipient is duplicated")
		}
		envelope, err := publisher.seal(message, recipient)
		if err != nil {
			return err
		}
		if err := publisher.remote.UploadEvent(ctx, publisher.identity.DeviceUUID, publisher.identity.Credential, envelope); err != nil {
			return fmt.Errorf("upload message event for installation %s: %w", recipient.InstallationID, err)
		}
	}
	return nil
}

func (publisher *Publisher) seal(message sms.ReceivedMessage, recipient incomingcall.Recipient) (callevent.Envelope, error) {
	if !identity.ValidDeviceUUID(recipient.InstallationID) || len(recipient.IdentityPublicKey) != ed25519.PublicKeySize || len(recipient.KeyAgreementPublicKey) != keyBytes {
		return callevent.Envelope{}, errors.New("message recipient identity is invalid")
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(publisher.identity.CommandPrivateKey)
	if err != nil {
		return callevent.Envelope{}, fmt.Errorf("parse device message key: %w", err)
	}
	publicKey, err := ecdh.X25519().NewPublicKey(recipient.KeyAgreementPublicKey)
	if err != nil {
		return callevent.Envelope{}, fmt.Errorf("parse recipient message key: %w", err)
	}
	sharedSecret, err := privateKey.ECDH(publicKey)
	if err != nil {
		return callevent.Envelope{}, fmt.Errorf("derive recipient message key: %w", err)
	}
	eventID := deterministicEventID(publisher.identity.DeviceUUID, message.ID, recipient.InstallationID)
	envelope := callevent.Envelope{
		ID: eventID, Type: eventType, DeviceUUID: publisher.identity.DeviceUUID,
		OccurredAt: message.Timestamp.UTC().Truncate(time.Millisecond), KeyEpoch: keyEpoch,
	}
	payload, err := json.Marshal(Payload{
		Version: protocolVersion, MessageID: message.ID, DeviceUUID: publisher.identity.DeviceUUID,
		SlotID: publisher.identity.SlotID, SIMID: publisher.identity.SIMID,
		NumericID: publisher.identity.NumericID, RemoteNumber: message.Sender, Body: message.Text,
		ReceivedAt: message.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Segments:   message.Segments, InstallationID: recipient.InstallationID,
	})
	if err != nil {
		return callevent.Envelope{}, fmt.Errorf("encode message event: %w", err)
	}
	aead, err := eventAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID)
	if err != nil {
		return callevent.Envelope{}, fmt.Errorf("create message event cipher: %w", err)
	}
	nonceHash := sha256.Sum256([]byte("sim-deck-message-nonce-v1\x00" + eventID))
	envelope.Nonce = append([]byte(nil), nonceHash[:nonceBytes]...)
	envelope.Ciphertext = aead.Seal(nil, envelope.Nonce, payload, associatedData(envelope))
	envelope.Signature = ed25519.Sign(publisher.identity.IdentityPrivateKey, signedData(envelope))
	return envelope, nil
}

func deterministicEventID(deviceID string, messageID string, installationID string) string {
	digest := sha256.Sum256([]byte(eventContext + "\x00" + deviceID + "\x00" + messageID + "\x00" + installationID))
	value := append([]byte(nil), digest[:16]...)
	value[6] = (value[6] & 0x0f) | 0x50
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func validateMessage(message sms.ReceivedMessage) error {
	if !messageIDPattern.MatchString(message.ID) || !phoneNumberPattern.MatchString(message.Sender) ||
		message.Timestamp.IsZero() || strings.TrimSpace(message.Text) == "" || message.Segments < 1 || message.Segments > 255 {
		return errors.New("complete incoming SMS is required")
	}
	return nil
}

func eventAEAD(sharedSecret []byte, eventID string, deviceID string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, sharedSecret, []byte(eventID+"\x00"+deviceID), eventContext, keyBytes)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func associatedData(envelope callevent.Envelope) []byte {
	buffer := make([]byte, 0, 192)
	buffer = appendBytes(buffer, []byte(envelope.ID))
	buffer = appendBytes(buffer, []byte(envelope.Type))
	buffer = appendBytes(buffer, []byte(envelope.DeviceUUID))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.OccurredAt.UTC().UnixNano()))
	return binary.BigEndian.AppendUint64(buffer, uint64(envelope.KeyEpoch))
}

func signedData(envelope callevent.Envelope) []byte {
	buffer := associatedData(envelope)
	buffer = appendBytes(buffer, envelope.Nonce)
	return appendBytes(buffer, envelope.Ciphertext)
}

func appendBytes(buffer []byte, value []byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...)
}
