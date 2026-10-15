package command

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
)

const (
	protocolVersion = 1
	keyBytes        = 32
	nonceBytes      = 12
)

// Envelope is one opaque command leased from the server.
type Envelope struct {
	ID              string    `json:"id"`
	DeviceUUID      string    `json:"device_uuid"`
	SenderPublicKey []byte    `json:"sender_public_key"`
	Nonce           []byte    `json:"nonce"`
	Ciphertext      []byte    `json:"ciphertext"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
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
	CallID     string `json:"call_id"`
	Number     string `json:"number,omitempty"`
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
	Claim(context.Context, string, string) (*Envelope, error)
	Complete(context.Context, string, string, string, EncryptedResult) error
}

// Calls is the modem call capability used by encrypted commands.
type Calls interface {
	Dial(context.Context, string) error
	Hangup(context.Context) error
	Hold(context.Context) error
	Resume(context.Context) error
	Swap(context.Context) error
	SendDTMF(context.Context, string) error
	State() call.State
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
	Events CallEventRegistrar
	Media  CallMedia
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
}

// NewRuntime creates a command runtime with a bounded idle polling interval.
func NewRuntime(remote Remote, state identity.State, calls Calls, pollInterval time.Duration, dependencies Dependencies) (*Runtime, error) {
	if remote == nil || calls == nil || state.Credential == "" || len(state.CommandPrivateKey) != keyBytes || pollInterval <= 0 {
		return nil, errors.New("command runtime requires a bound identity, remote, call controller, and positive poll interval")
	}
	if dependencies.Events == nil || dependencies.Media == nil {
		return nil, errors.New("command runtime requires call event and media dependencies")
	}
	return &Runtime{
		remote: remote, identity: state, calls: calls, pollInterval: pollInterval,
		random: rand.Reader, events: dependencies.Events, media: dependencies.Media,
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
	if payload.Version != protocolVersion || strings.TrimSpace(payload.CallID) == "" {
		return "", errInvalidCommand
	}
	switch payload.Operation {
	case "call.start":
		if strings.TrimSpace(payload.MediaOffer) == "" {
			return "", errInvalidCommand
		}
		answer, err := runtime.media.Negotiate(ctx, payload.CallID, payload.MediaOffer)
		if err != nil {
			return "", fmt.Errorf("%w: %v", errMediaUnavailable, err)
		}
		if err := runtime.calls.Dial(ctx, payload.Number); err != nil {
			_ = runtime.media.Stop(payload.CallID)
			return "", err
		}
		return answer, nil
	case "call.end":
		if err := runtime.calls.Hangup(ctx); err != nil {
			return "", err
		}
		_ = runtime.media.Stop(payload.CallID)
		return "", nil
	case "call.hold":
		return "", runtime.calls.Hold(ctx)
	case "call.resume":
		return "", runtime.calls.Resume(ctx)
	case "call.swap":
		return "", runtime.calls.Swap(ctx)
	case "call.dtmf":
		return "", runtime.calls.SendDTMF(ctx, payload.Digits)
	default:
		return "", errInvalidCommand
	}
}

var (
	errInvalidCommand   = errors.New("invalid command payload")
	errMediaUnavailable = errors.New("call media unavailable")
)

func commandErrorCode(err error) string {
	if errors.Is(err, errInvalidCommand) {
		return "invalid_command"
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
