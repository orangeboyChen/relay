package command

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
)

const (
	protocolVersion = 1
	keyBytes        = 32
	nonceBytes      = 12
)

// Envelope is one opaque command leased from the server.
type Envelope struct {
	ID                   string    `json:"id"`
	DeviceUUID           string    `json:"device_uuid"`
	SenderInstallationID string    `json:"sender_installation_id"`
	SenderPublicKey      []byte    `json:"sender_public_key"`
	Nonce                []byte    `json:"nonce"`
	Ciphertext           []byte    `json:"ciphertext"`
	Signature            []byte    `json:"signature"`
	CreatedAt            time.Time `json:"created_at"`
	ExpiresAt            time.Time `json:"expires_at"`
}

// EncryptedResult is the opaque completion returned through the server.
type EncryptedResult struct {
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

// Payload is the command plaintext visible only to the iPhone and target device.
type Payload struct {
	Version    int    `json:"version"`
	Operation  string `json:"operation"`
	SlotID     string `json:"slot_id,omitempty"`
	SIMID      string `json:"sim_id,omitempty"`
	CallID     string `json:"call_id,omitempty"`
	MessageID  string `json:"message_id,omitempty"`
	Number     string `json:"number,omitempty"`
	Body       string `json:"body,omitempty"`
	Digits     string `json:"digits,omitempty"`
	MediaOffer string `json:"media_offer,omitempty"`
}

// Result is the encrypted execution outcome consumed by the initiating iPhone.
type Result struct {
	Version     int        `json:"version"`
	Success     bool       `json:"success"`
	ErrorCode   string     `json:"error_code,omitempty"`
	CallState   call.State `json:"call_state"`
	MediaAnswer string     `json:"media_answer,omitempty"`
}

// Remote provides authenticated opaque command delivery.
type Remote interface {
	ListInstallationRecipients(context.Context, string, string) ([]incomingcall.Recipient, error)
	Claim(context.Context, string, string) (*Envelope, error)
	Complete(context.Context, string, string, string, EncryptedResult) error
}

// Calls is the modem call capability used by encrypted commands.
type Calls interface {
	Dial(context.Context, string) error
	Answer(context.Context) error
	AnswerWaiting(context.Context) error
	Reject(context.Context) error
	Hangup(context.Context) error
	Hold(context.Context) error
	Resume(context.Context) error
	Swap(context.Context) error
	SendDTMF(context.Context, string) error
	State() call.State
}

// Messages is the modem SMS capability used by encrypted commands.
type Messages interface {
	SendSMS(context.Context, string, string, byte) error
}

// CallEventRegistrar binds modem state changes to the initiating encrypted call.
type CallEventRegistrar interface {
	RegisterOutgoing(string, []byte) error
	Unregister(string)
}

// CallMedia negotiates and owns encrypted audio for one cellular call.
type CallMedia interface {
	Negotiate(context.Context, string, string) (string, error)
	Stop(string) error
}

// Dependencies contains the endpoint capabilities used by the command runtime.
type Dependencies struct {
	Events   CallEventRegistrar
	Media    CallMedia
	Messages Messages
}

// Runtime polls, decrypts, executes, and acknowledges commands for one modem.
type Runtime struct {
	remote       Remote
	identity     identity.State
	calls        Calls
	pollInterval time.Duration
	random       io.Reader
	events       CallEventRegistrar
	media        CallMedia
	messages     Messages
	now          func() time.Time
}

// NewRuntime creates a command runtime with a bounded idle polling interval.
func NewRuntime(remote Remote, state identity.State, calls Calls, pollInterval time.Duration, dependencies Dependencies) (*Runtime, error) {
	if remote == nil || calls == nil || state.Credential == "" || len(state.CommandPrivateKey) != keyBytes || pollInterval <= 0 {
		return nil, errors.New("command runtime requires a bound identity, remote, call controller, and positive poll interval")
	}
	if dependencies.Events == nil || dependencies.Media == nil || dependencies.Messages == nil {
		return nil, errors.New("command runtime requires call event, media, and message dependencies")
	}
	return &Runtime{
		remote: remote, identity: state, calls: calls, pollInterval: pollInterval,
		random: rand.Reader, events: dependencies.Events, media: dependencies.Media, messages: dependencies.Messages, now: time.Now,
	}, nil
}

// Run processes commands until cancellation or a server transport failure.
func (runtime *Runtime) Run(ctx context.Context) error {
	for {
		envelope, err := runtime.remote.Claim(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential)
		if err != nil {
			return err
		}
		if envelope == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(runtime.pollInterval):
				continue
			}
		}
		if err := runtime.process(ctx, *envelope); err != nil {
			return err
		}
	}
}

func (runtime *Runtime) process(ctx context.Context, envelope Envelope) error {
	recipients, err := runtime.remote.ListInstallationRecipients(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential)
	if err != nil {
		return fmt.Errorf("load trusted command senders: %w", err)
	}
	var verificationKey ed25519.PublicKey
	for _, recipient := range recipients {
		if recipient.InstallationID == envelope.SenderInstallationID {
			verificationKey = ed25519.PublicKey(recipient.IdentityPublicKey)
			break
		}
	}
	if err := verifyEnvelope(envelope, verificationKey, runtime.now()); err != nil {
		return fmt.Errorf("verify command %s: %w", envelope.ID, err)
	}
	payload, sharedSecret, err := open(runtime.identity, envelope)
	result := Result{Version: protocolVersion, CallState: runtime.calls.State()}
	if err != nil {
		return fmt.Errorf("decrypt command %s: %w", envelope.ID, err)
	}
	registered := payload.Version == protocolVersion && payload.Operation == "call.start" && strings.TrimSpace(payload.CallID) != "" && strings.TrimSpace(payload.MediaOffer) != ""
	if registered {
		if err := runtime.events.RegisterOutgoing(payload.CallID, sharedSecret); err != nil {
			return fmt.Errorf("register call events for command %s: %w", envelope.ID, err)
		}
	}
	mediaAnswer, executionError := runtime.execute(ctx, payload)
	if executionError != nil {
		if registered {
			runtime.events.Unregister(payload.CallID)
		}
		result.ErrorCode = commandErrorCode(executionError)
		result.CallState = runtime.calls.State()
	} else {
		result.Success = true
		result.CallState = runtime.calls.State()
		result.MediaAnswer = mediaAnswer
	}
	encrypted, err := sealResult(runtime.random, sharedSecret, envelope, result)
	if err != nil {
		return fmt.Errorf("encrypt command result %s: %w", envelope.ID, err)
	}
	if err := runtime.remote.Complete(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential, envelope.ID, encrypted); err != nil {
		return fmt.Errorf("complete command %s: %w", envelope.ID, err)
	}
	return nil
}

func (runtime *Runtime) execute(ctx context.Context, payload Payload) (string, error) {
	if payload.Version != protocolVersion {
		return "", errInvalidCommand
	}
	if !runtime.targetMatches(payload) {
		return "", errWrongSlot
	}
	switch payload.Operation {
	case "message.send":
		if !identity.ValidDeviceUUID(payload.MessageID) || strings.TrimSpace(payload.Number) == "" || strings.TrimSpace(payload.Body) == "" {
			return "", errInvalidCommand
		}
		reference := sha256.Sum256([]byte(payload.MessageID))
		return "", runtime.messages.SendSMS(ctx, payload.Number, payload.Body, reference[0])
	case "call.start":
		if strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.MediaOffer) == "" {
			return "", errInvalidCommand
		}
		if err := runtime.calls.Dial(ctx, payload.Number); err != nil {
			_ = runtime.media.Stop(payload.CallID)
			return "", err
		}
		answer, err := runtime.media.Negotiate(ctx, payload.CallID, payload.MediaOffer)
		if err != nil {
			// The modem is already dialing; release it if media negotiation cannot complete.
			_ = runtime.calls.Hangup(context.WithoutCancel(ctx))
			_ = runtime.media.Stop(payload.CallID)
			return "", fmt.Errorf("%w: %v", errMediaUnavailable, err)
		}
		return answer, nil
	case "call.answer":
		if strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.MediaOffer) == "" {
			return "", errInvalidCommand
		}
		answer, err := runtime.media.Negotiate(ctx, payload.CallID, payload.MediaOffer)
		if err != nil {
			return "", fmt.Errorf("%w: %v", errMediaUnavailable, err)
		}
		if runtime.calls.State() == call.StateWaiting {
			err = runtime.calls.AnswerWaiting(ctx)
		} else {
			err = runtime.calls.Answer(ctx)
		}
		if err != nil {
			_ = runtime.media.Stop(payload.CallID)
			return "", err
		}
		return answer, nil
	case "call.reject":
		if strings.TrimSpace(payload.CallID) == "" {
			return "", errInvalidCommand
		}
		return "", runtime.calls.Reject(ctx)
	case "call.end":
		if strings.TrimSpace(payload.CallID) == "" {
			return "", errInvalidCommand
		}
		if err := runtime.calls.Hangup(ctx); err != nil {
			return "", err
		}
		_ = runtime.media.Stop(payload.CallID)
		return "", nil
	case "call.hold":
		if strings.TrimSpace(payload.CallID) == "" {
			return "", errInvalidCommand
		}
		return "", runtime.calls.Hold(ctx)
	case "call.resume":
		if strings.TrimSpace(payload.CallID) == "" {
			return "", errInvalidCommand
		}
		return "", runtime.calls.Resume(ctx)
	case "call.swap":
		if strings.TrimSpace(payload.CallID) == "" {
			return "", errInvalidCommand
		}
		return "", runtime.calls.Swap(ctx)
	case "call.dtmf":
		if strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.Digits) == "" {
			return "", errInvalidCommand
		}
		return "", runtime.calls.SendDTMF(ctx, payload.Digits)
	default:
		return "", errInvalidCommand
	}
}

func (runtime *Runtime) targetMatches(payload Payload) bool {
	if payload.SlotID != "" && payload.SlotID != runtime.identity.SlotID {
		return false
	}
	if payload.SIMID != "" && payload.SIMID != runtime.identity.SIMID {
		return false
	}
	return true
}

var (
	errInvalidCommand   = errors.New("invalid command payload")
	errWrongSlot        = errors.New("command targets another SIM slot")
	errMediaUnavailable = errors.New("call media unavailable")
)

func commandErrorCode(err error) string {
	if errors.Is(err, errInvalidCommand) {
		return "invalid_command"
	}
	if errors.Is(err, errWrongSlot) {
		return "wrong_sim_slot"
	}
	if errors.Is(err, errMediaUnavailable) {
		return "media_unavailable"
	}
	return "modem_rejected"
}

func open(state identity.State, envelope Envelope) (Payload, []byte, error) {
	if envelope.DeviceUUID != state.DeviceUUID || len(envelope.SenderPublicKey) != keyBytes || len(envelope.Nonce) != nonceBytes || len(envelope.Ciphertext) == 0 {
		return Payload{}, nil, errInvalidCommand
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(state.CommandPrivateKey)
	if err != nil {
		return Payload{}, nil, err
	}
	publicKey, err := ecdh.X25519().NewPublicKey(envelope.SenderPublicKey)
	if err != nil {
		return Payload{}, nil, err
	}
	sharedSecret, err := privateKey.ECDH(publicKey)
	if err != nil {
		return Payload{}, nil, err
	}
	aead, err := commandAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID, "sim-deck-command-v1")
	if err != nil {
		return Payload{}, nil, err
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData("sim-deck-command-v1", envelope.ID, envelope.DeviceUUID))
	if err != nil {
		return Payload{}, nil, err
	}
	var payload Payload
	decoderError := json.Unmarshal(plaintext, &payload)
	if decoderError != nil {
		return Payload{}, nil, errInvalidCommand
	}
	return payload, sharedSecret, nil
}

func verifyEnvelope(envelope Envelope, verificationKey ed25519.PublicKey, now time.Time) error {
	if !identity.ValidDeviceUUID(envelope.ID) || !identity.ValidDeviceUUID(envelope.DeviceUUID) ||
		!identity.ValidDeviceUUID(envelope.SenderInstallationID) || len(verificationKey) != ed25519.PublicKeySize ||
		len(envelope.SenderPublicKey) != keyBytes || len(envelope.Nonce) != nonceBytes || len(envelope.Ciphertext) == 0 ||
		len(envelope.Signature) != ed25519.SignatureSize || envelope.CreatedAt.IsZero() ||
		!envelope.ExpiresAt.After(now) || envelope.ExpiresAt.After(envelope.CreatedAt.Add(5*time.Minute)) {
		return errInvalidCommand
	}
	if !ed25519.Verify(verificationKey, commandSignedData(envelope), envelope.Signature) {
		return errInvalidCommand
	}
	return nil
}

func commandSignedData(envelope Envelope) []byte {
	buffer := make([]byte, 0, 256+len(envelope.Ciphertext))
	for _, field := range [][]byte{
		[]byte("sim-deck-command-envelope-v2"), []byte(envelope.ID), []byte(envelope.DeviceUUID),
		[]byte(envelope.SenderInstallationID), envelope.SenderPublicKey, envelope.Nonce, envelope.Ciphertext,
	} {
		buffer = appendCommandField(buffer, field)
	}
	buffer = binary.BigEndian.AppendUint64(buffer, uint64(envelope.CreatedAt.UTC().UnixMilli()))
	return binary.BigEndian.AppendUint64(buffer, uint64(envelope.ExpiresAt.UTC().UnixMilli()))
}

func appendCommandField(buffer []byte, value []byte) []byte {
	buffer = binary.BigEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...)
}

func sealResult(random io.Reader, sharedSecret []byte, envelope Envelope, result Result) (EncryptedResult, error) {
	plaintext, err := json.Marshal(result)
	if err != nil {
		return EncryptedResult{}, err
	}
	aead, err := commandAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID, "sim-deck-command-result-v1")
	if err != nil {
		return EncryptedResult{}, err
	}
	nonce := make([]byte, nonceBytes)
	if _, err := io.ReadFull(random, nonce); err != nil {
		return EncryptedResult{}, err
	}
	return EncryptedResult{
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, associatedData("sim-deck-command-result-v1", envelope.ID, envelope.DeviceUUID)),
	}, nil
}

func commandAEAD(sharedSecret []byte, commandID string, deviceID string, context string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, sharedSecret, []byte(commandID+"\x00"+deviceID), context, keyBytes)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func associatedData(context string, commandID string, deviceID string) []byte {
	return []byte(context + "\x00" + commandID + "\x00" + deviceID)
}
