package callevent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
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
	"sync"
	"time"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
)

const (
	eventType       = "call.state"
	protocolVersion = 1
	keyEpoch        = 1
	keyBytes        = 32
	nonceBytes      = 12
	eventContext    = "sim-deck-call-event-v1"
)

// Envelope is the opaque, device-signed event stored by the server.
type Envelope struct {
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

// Payload is visible only to the device and the iPhone that started the call.
type Payload struct {
	Version    int        `json:"version"`
	CallID     string     `json:"call_id"`
	DeviceUUID string     `json:"device_uuid,omitempty"`
	SlotID     string     `json:"slot_id,omitempty"`
	SIMID      string     `json:"sim_id,omitempty"`
	State      call.State `json:"state"`
	Active     bool       `json:"active"`
	Held       bool       `json:"held"`
	Waiting    bool       `json:"waiting"`
}

// Remote uploads a signed opaque event with device authentication.
type Remote interface {
	UploadEvent(context.Context, string, string, Envelope) error
}

type callContext struct {
	id           string
	sharedSecret []byte
}

// Publisher encrypts modem state transitions for the iPhone that initiated the call.
type Publisher struct {
	remote    Remote
	identity  identity.State
	snapshots <-chan call.Snapshot
	random    io.Reader
	now       func() time.Time

	mu      sync.RWMutex
	current *callContext
}

// NewPublisher creates an authenticated call-event publisher.
func NewPublisher(remote Remote, state identity.State, snapshots <-chan call.Snapshot) (*Publisher, error) {
	if remote == nil || snapshots == nil || state.Credential == "" || len(state.IdentityPrivateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("call event publisher requires a bound identity, remote, and call-state source")
	}
	return &Publisher{
		remote: remote, identity: state, snapshots: snapshots, random: rand.Reader, now: time.Now,
	}, nil
}

// RegisterOutgoing binds future modem transitions to one end-to-end encrypted call.
func (publisher *Publisher) RegisterOutgoing(callID string, sharedSecret []byte) error {
	return publisher.register(callID, sharedSecret)
}

// RegisterIncoming binds future modem transitions to an incoming encrypted call.
func (publisher *Publisher) RegisterIncoming(callID string, sharedSecret []byte) error {
	return publisher.register(callID, sharedSecret)
}

func (publisher *Publisher) register(callID string, sharedSecret []byte) error {
	if callID == "" || len(sharedSecret) != keyBytes {
		return errors.New("call id and 32-byte shared secret are required")
	}
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	publisher.current = &callContext{id: callID, sharedSecret: append([]byte(nil), sharedSecret...)}
	return nil
}

// Unregister removes a failed call command before it can emit modem state.
func (publisher *Publisher) Unregister(callID string) {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	if publisher.current != nil && publisher.current.id == callID {
		publisher.current = nil
	}
}

// Run uploads every state transition until cancellation or a delivery failure.
func (publisher *Publisher) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case snapshot, ok := <-publisher.snapshots:
			if !ok {
				return errors.New("call event snapshot stream closed")
			}
			if err := publisher.publish(ctx, snapshot); err != nil {
				return err
			}
		}
	}
}

func (publisher *Publisher) publish(ctx context.Context, snapshot call.Snapshot) error {
	registered := publisher.registeredCall()
	if registered == nil {
		return nil
	}
	eventID, err := randomUUID(publisher.random)
	if err != nil {
		return fmt.Errorf("generate call event id: %w", err)
	}
	envelope := Envelope{
		ID: eventID, Type: eventType, DeviceUUID: publisher.identity.DeviceUUID,
		OccurredAt: publisher.now().UTC().Truncate(time.Millisecond), KeyEpoch: keyEpoch,
	}
	payload, err := json.Marshal(Payload{
		Version: protocolVersion, CallID: registered.id, State: snapshot.State,
		DeviceUUID: publisher.identity.DeviceUUID, SlotID: publisher.identity.SlotID, SIMID: publisher.identity.SIMID,
		Active: snapshot.Active, Held: snapshot.Held, Waiting: snapshot.Waiting,
	})
	if err != nil {
		return fmt.Errorf("encode call event: %w", err)
	}
	envelope, err = seal(publisher.random, registered.sharedSecret, publisher.identity.IdentityPrivateKey, envelope, payload)
	if err != nil {
		return fmt.Errorf("encrypt call event: %w", err)
	}
	if err := publisher.remote.UploadEvent(ctx, publisher.identity.DeviceUUID, publisher.identity.Credential, envelope); err != nil {
		return fmt.Errorf("upload call event: %w", err)
	}
	if snapshot.State == call.StateEnded {
		publisher.Unregister(registered.id)
	}
	return nil
}

func (publisher *Publisher) registeredCall() *callContext {
	publisher.mu.RLock()
	defer publisher.mu.RUnlock()
	if publisher.current == nil {
		return nil
	}
	return &callContext{id: publisher.current.id, sharedSecret: append([]byte(nil), publisher.current.sharedSecret...)}
}

func seal(random io.Reader, sharedSecret []byte, signingKey ed25519.PrivateKey, envelope Envelope, plaintext []byte) (Envelope, error) {
	aead, err := eventAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID)
	if err != nil {
		return Envelope{}, err
	}
	nonce := make([]byte, nonceBytes)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return Envelope{}, err
	}
	envelope.Nonce = nonce
	envelope.Ciphertext = aead.Seal(nil, nonce, plaintext, associatedData(envelope))
	envelope.Signature = ed25519.Sign(signingKey, signedData(envelope))
	return envelope, nil
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

func associatedData(envelope Envelope) []byte {
	buffer := make([]byte, 0, 128)
	buffer = appendBytes(buffer, []byte(envelope.ID))
	buffer = appendBytes(buffer, []byte(envelope.Type))
	buffer = appendBytes(buffer, []byte(envelope.DeviceUUID))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.OccurredAt.UTC().UnixNano()))
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.KeyEpoch))
	return buffer
}

func signedData(envelope Envelope) []byte {
	buffer := associatedData(envelope)
	buffer = appendBytes(buffer, envelope.Nonce)
	return appendBytes(buffer, envelope.Ciphertext)
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
