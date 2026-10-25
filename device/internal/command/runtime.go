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
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/slotpolicy"
)

const (
	protocolVersion     = 1
	keyBytes            = 32
	nonceBytes          = 12
	defaultCallLease    = 30 * time.Second
	callCleanupTimeout  = 5 * time.Second
	callCleanupAttempts = 3
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
	Version              int                `json:"version"`
	Operation            string             `json:"operation"`
	SlotID               string             `json:"slot_id,omitempty"`
	SIMID                string             `json:"sim_id,omitempty"`
	CallID               string             `json:"call_id,omitempty"`
	MessageID            string             `json:"message_id,omitempty"`
	Number               string             `json:"number,omitempty"`
	Body                 string             `json:"body,omitempty"`
	Digits               string             `json:"digits,omitempty"`
	MediaOffer           string             `json:"media_offer,omitempty"`
	CallLeaseSeconds     int                `json:"call_lease_seconds,omitempty"`
	DataEnabled          *bool              `json:"data_enabled,omitempty"`
	AirplaneModeEnabled  *bool              `json:"airplane_mode_enabled,omitempty"`
	IncomingCallsEnabled *bool              `json:"incoming_calls_enabled,omitempty"`
	IncomingSMSEnabled   *bool              `json:"incoming_sms_enabled,omitempty"`
	VoLTEEnabled         *bool              `json:"volte_enabled,omitempty"`
	ESIMProfileID        string             `json:"esim_profile_id,omitempty"`
	ESIMProfileName      string             `json:"esim_profile_name,omitempty"`
	Attachment           *MessageAttachment `json:"attachment,omitempty"`
}

// MessageAttachment is one inline encrypted MMS object.
type MessageAttachment struct {
	Filename string `json:"filename"`
	MIMEType string `json:"mime_type"`
	Data     []byte `json:"data"`
}

// Result is the encrypted execution outcome consumed by the initiating iPhone.
type Result struct {
	Version      int                  `json:"version"`
	Success      bool                 `json:"success"`
	ErrorCode    string               `json:"error_code,omitempty"`
	CallState    call.State           `json:"call_state"`
	MediaAnswer  string               `json:"media_answer,omitempty"`
	SlotSettings *slotpolicy.Settings `json:"slot_settings,omitempty"`
	ESIMProfiles *[]ESIMProfile       `json:"esim_profiles,omitempty"`
}

// ESIMProfile is the modem-owned view of one installed eSIM profile.
// Activation codes are intentionally never returned or persisted here.
type ESIMProfile struct {
	ProfileID string `json:"profile_id"`
	ICCID     string `json:"iccid"`
	Enabled   bool   `json:"enabled"`
	Nickname  string `json:"nickname,omitempty"`
	Provider  string `json:"provider,omitempty"`
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

// ESIMProfiles exposes only installed-profile operations. Provisioning a new
// profile requires a modem-specific LPA flow and is deliberately not guessed.
type ESIMProfiles interface {
	ListESIMProfiles(context.Context) ([]ESIMProfile, error)
	EnableESIMProfile(context.Context, string) error
	DisableESIMProfile(context.Context, string) error
	DeleteESIMProfile(context.Context, string) error
	RenameESIMProfile(context.Context, string, string) error
}

type mmsMessages interface {
	SendMMS(context.Context, string, string, MessageAttachment) error
}

// CallEventRegistrar binds modem state changes to the initiating encrypted call.
type CallEventRegistrar interface {
	RegisterOutgoing(string, []byte) error
	Unregister(string)
}

// CallMedia negotiates and owns encrypted audio for one cellular call.
type CallMedia interface {
	Negotiate(context.Context, string, string) (string, error)
	Renegotiate(context.Context, string, string) (string, error)
	Stop(string) error
}

// SlotSettings applies owner policy that is visible only inside encrypted commands.
type SlotSettings interface {
	Current() slotpolicy.Settings
	Configure(context.Context, slotpolicy.Update) (slotpolicy.Settings, error)
	RestartModem(context.Context) error
}

// Dependencies contains the endpoint capabilities used by the command runtime.
type Dependencies struct {
	Events       CallEventRegistrar
	Media        CallMedia
	Messages     Messages
	ESIMProfiles ESIMProfiles
	SlotSettings SlotSettings
	Logger       *slog.Logger
}

// Runtime polls, decrypts, executes, and acknowledges commands for one modem.
type Runtime struct {
	remote          Remote
	identity        identity.State
	calls           Calls
	pollInterval    time.Duration
	random          io.Reader
	events          CallEventRegistrar
	media           CallMedia
	messages        Messages
	esimProfiles    ESIMProfiles
	slotSettings    SlotSettings
	now             func() time.Time
	logger          *slog.Logger
	activeCallLease *callLease
}

type callLease struct {
	callID               string
	senderInstallationID string
	duration             time.Duration
	expiresAt            time.Time
}

// NewRuntime creates a command runtime with a bounded idle polling interval.
func NewRuntime(remote Remote, state identity.State, calls Calls, pollInterval time.Duration, dependencies Dependencies) (*Runtime, error) {
	if remote == nil || calls == nil || state.Credential == "" || len(state.CommandPrivateKey) != keyBytes || pollInterval <= 0 {
		return nil, errors.New("command runtime requires a bound identity, remote, call controller, and positive poll interval")
	}
	if dependencies.Events == nil || dependencies.Media == nil || dependencies.Messages == nil || dependencies.SlotSettings == nil {
		return nil, errors.New("command runtime requires call event, media, message, and slot settings dependencies")
	}
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Runtime{
		remote: remote, identity: state, calls: calls, pollInterval: pollInterval,
		random: rand.Reader, events: dependencies.Events, media: dependencies.Media, messages: dependencies.Messages,
		esimProfiles: dependencies.ESIMProfiles,
		slotSettings: dependencies.SlotSettings, now: time.Now,
		logger: logger,
	}, nil
}

// Run processes commands until cancellation. Transient server failures are
// retried so an active WebRTC call can survive a short control-plane outage.
func (runtime *Runtime) Run(ctx context.Context) (runError error) {
	defer func() {
		if err := runtime.releaseActiveCall("command runtime stopped"); err != nil {
			runError = errors.Join(runError, err)
		}
	}()
	for {
		if err := runtime.expireCallLease(); err != nil {
			return err
		}
		envelope, err := runtime.remote.Claim(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			runtime.logger.Warn(
				"encrypted device command claim deferred",
				"device_uuid", runtime.identity.DeviceUUID,
				"active_call", runtime.activeCallLease != nil,
				"error", err,
			)
			if err := runtime.waitForNextPoll(ctx); err != nil {
				return err
			}
			continue
		}
		if envelope == nil {
			if err := runtime.waitForNextPoll(ctx); err != nil {
				return err
			}
			continue
		}
		runtime.logger.Info("encrypted device command claimed", "command_id", envelope.ID, "sender_installation_id", envelope.SenderInstallationID)
		if err := runtime.process(ctx, *envelope); err != nil {
			runtime.logger.Error("encrypted device command processing failed", "command_id", envelope.ID, "sender_installation_id", envelope.SenderInstallationID, "error", err)
			return err
		}
	}
}

func (runtime *Runtime) waitForNextPoll(ctx context.Context) error {
	wait := runtime.pollInterval
	if runtime.activeCallLease != nil {
		remaining := runtime.activeCallLease.expiresAt.Sub(runtime.now())
		if remaining < wait {
			wait = max(remaining, 0)
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
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
	execution, executionError := runtime.execute(ctx, payload, envelope.SenderInstallationID)
	if executionError != nil {
		if registered {
			runtime.events.Unregister(payload.CallID)
		}
		result.ErrorCode = commandErrorCode(executionError)
		result.CallState = runtime.calls.State()
		logArguments := []any{
			"command_id", envelope.ID,
			"operation", payload.Operation,
			"call_id", payload.CallID,
			"message_id", payload.MessageID,
			"error_code", result.ErrorCode,
			"error", executionError,
		}
		if errors.Is(executionError, errWrongSlot) {
			logArguments = append(logArguments,
				"received_slot_id", payload.SlotID,
				"expected_slot_id", runtime.identity.SlotID,
				"received_sim_id", payload.SIMID,
				"expected_sim_id", runtime.identity.SIMID,
			)
		}
		runtime.logger.Error("device command execution failed", logArguments...)
	} else {
		result.Success = true
		result.CallState = runtime.calls.State()
		result.MediaAnswer = execution.mediaAnswer
		result.SlotSettings = execution.slotSettings
		result.ESIMProfiles = execution.esimProfiles
		runtime.updateCallLease(payload, envelope.SenderInstallationID)
	}
	encrypted, err := sealResult(runtime.random, sharedSecret, envelope, result)
	if err != nil {
		return fmt.Errorf("encrypt command result %s: %w", envelope.ID, err)
	}
	if err := runtime.remote.Complete(ctx, runtime.identity.DeviceUUID, runtime.identity.Credential, envelope.ID, encrypted); err != nil {
		return fmt.Errorf("complete command %s: %w", envelope.ID, err)
	}
	runtime.logger.Info(
		"device command completed",
		"command_id", envelope.ID,
		"operation", payload.Operation,
		"call_id", payload.CallID,
		"message_id", payload.MessageID,
		"success", result.Success,
		"error_code", result.ErrorCode,
		"call_state", result.CallState,
	)
	return nil
}

type executionResult struct {
	mediaAnswer  string
	slotSettings *slotpolicy.Settings
	esimProfiles *[]ESIMProfile
}

func (runtime *Runtime) execute(ctx context.Context, payload Payload, senderInstallationID string) (executionResult, error) {
	if payload.Version != protocolVersion {
		return executionResult{}, errInvalidCommand
	}
	if !runtime.targetMatches(payload) {
		return executionResult{}, errWrongSlot
	}
	switch payload.Operation {
	case "esim.list":
		if runtime.esimProfiles == nil {
			return executionResult{}, errESIMUnavailable
		}
		profiles, err := runtime.esimProfiles.ListESIMProfiles(ctx)
		if err != nil {
			return executionResult{}, err
		}
		return executionResult{esimProfiles: &profiles}, nil
	case "esim.enable", "esim.disable", "esim.delete", "esim.rename":
		if runtime.esimProfiles == nil || strings.TrimSpace(payload.ESIMProfileID) == "" {
			return executionResult{}, errESIMUnavailable
		}
		profileID := strings.TrimSpace(payload.ESIMProfileID)
		var err error
		switch payload.Operation {
		case "esim.enable":
			err = runtime.esimProfiles.EnableESIMProfile(ctx, profileID)
		case "esim.disable":
			err = runtime.esimProfiles.DisableESIMProfile(ctx, profileID)
		case "esim.delete":
			err = runtime.esimProfiles.DeleteESIMProfile(ctx, profileID)
		case "esim.rename":
			if strings.TrimSpace(payload.ESIMProfileName) == "" {
				return executionResult{}, errInvalidCommand
			}
			err = runtime.esimProfiles.RenameESIMProfile(ctx, profileID, payload.ESIMProfileName)
		}
		if err != nil {
			return executionResult{}, err
		}
		return executionResult{}, nil
	case "slot.configure":
		settings, err := runtime.slotSettings.Configure(ctx, slotpolicy.Update{
			DataEnabled:          payload.DataEnabled,
			AirplaneModeEnabled:  payload.AirplaneModeEnabled,
			IncomingCallsEnabled: payload.IncomingCallsEnabled,
			IncomingSMSEnabled:   payload.IncomingSMSEnabled,
			VoLTEEnabled:         payload.VoLTEEnabled,
		})
		if err != nil {
			return executionResult{}, err
		}
		return executionResult{slotSettings: &settings}, nil
	case "slot.restart_modem":
		if err := runtime.slotSettings.RestartModem(ctx); err != nil {
			return executionResult{}, err
		}
		return executionResult{}, nil
	case "message.send":
		if !identity.ValidDeviceUUID(payload.MessageID) || strings.TrimSpace(payload.Number) == "" ||
			(strings.TrimSpace(payload.Body) == "" && payload.Attachment == nil) {
			return executionResult{}, errInvalidCommand
		}
		if payload.Attachment != nil &&
			(strings.TrimSpace(payload.Attachment.Filename) == "" ||
				payload.Attachment.MIMEType != "image/jpeg" ||
				len(payload.Attachment.Data) == 0 || len(payload.Attachment.Data) > 40_000) {
			return executionResult{}, errInvalidCommand
		}
		reference := sha256.Sum256([]byte(payload.MessageID))
		if registrar, ok := runtime.messages.(interface{ PrepareMessageStatus(string) }); ok {
			registrar.PrepareMessageStatus(payload.MessageID)
		}
		if payload.Attachment != nil {
			messages, ok := runtime.messages.(mmsMessages)
			if !ok {
				return executionResult{}, errMMSUnavailable
			}
			return executionResult{}, messages.SendMMS(ctx, payload.Number, payload.Body, *payload.Attachment)
		}
		return executionResult{}, runtime.messages.SendSMS(ctx, payload.Number, payload.Body, reference[0])
	case "call.start":
		if strings.TrimSpace(payload.CallID) == "" ||
			strings.TrimSpace(payload.Number) == "" ||
			strings.TrimSpace(payload.MediaOffer) == "" || !validCallLeaseSeconds(payload.CallLeaseSeconds) {
			return executionResult{}, errInvalidCommand
		}
		if err := runtime.calls.Dial(ctx, payload.Number); err != nil {
			_ = runtime.media.Stop(payload.CallID)
			return executionResult{}, err
		}
		answer, err := runtime.media.Negotiate(ctx, payload.CallID, payload.MediaOffer)
		if err != nil {
			// The modem is already dialing; release it if media negotiation cannot complete.
			_ = runtime.calls.Hangup(context.WithoutCancel(ctx))
			_ = runtime.media.Stop(payload.CallID)
			return executionResult{}, fmt.Errorf("%w: %v", errMediaUnavailable, err)
		}
		return executionResult{mediaAnswer: answer}, nil
	case "call.answer":
		if strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.MediaOffer) == "" ||
			!validCallLeaseSeconds(payload.CallLeaseSeconds) {
			return executionResult{}, errInvalidCommand
		}
		answer, err := runtime.media.Negotiate(ctx, payload.CallID, payload.MediaOffer)
		if err != nil {
			_ = runtime.media.Stop(payload.CallID)
			_ = runtime.calls.Reject(context.WithoutCancel(ctx))
			return executionResult{}, fmt.Errorf("%w: %v", errMediaUnavailable, err)
		}
		if runtime.calls.State() == call.StateWaiting {
			err = runtime.calls.AnswerWaiting(ctx)
		} else {
			err = runtime.calls.Answer(ctx)
		}
		if err != nil {
			_ = runtime.media.Stop(payload.CallID)
			_ = runtime.calls.Reject(context.WithoutCancel(ctx))
			return executionResult{}, err
		}
		return executionResult{mediaAnswer: answer}, nil
	case "call.heartbeat":
		if strings.TrimSpace(payload.CallID) == "" || runtime.activeCallLease == nil ||
			runtime.activeCallLease.callID != payload.CallID ||
			runtime.activeCallLease.senderInstallationID != senderInstallationID {
			return executionResult{}, errInvalidCommand
		}
		return executionResult{}, nil
	case "call.media.restart":
		if strings.TrimSpace(payload.CallID) == "" || strings.TrimSpace(payload.MediaOffer) == "" ||
			runtime.activeCallLease == nil || runtime.activeCallLease.callID != payload.CallID ||
			runtime.activeCallLease.senderInstallationID != senderInstallationID {
			return executionResult{}, errInvalidCommand
		}
		answer, err := runtime.media.Renegotiate(ctx, payload.CallID, payload.MediaOffer)
		if err != nil {
			return executionResult{}, fmt.Errorf("%w: %v", errMediaUnavailable, err)
		}
		return executionResult{mediaAnswer: answer}, nil
	case "call.reject":
		if strings.TrimSpace(payload.CallID) == "" {
			return executionResult{}, errInvalidCommand
		}
		if err := runtime.calls.Reject(ctx); err != nil {
			return executionResult{}, err
		}
		_ = runtime.media.Stop(payload.CallID)
		return executionResult{}, nil
	case "call.end":
		if strings.TrimSpace(payload.CallID) == "" {
			return executionResult{}, errInvalidCommand
		}
		if err := runtime.calls.Hangup(ctx); err != nil {
			return executionResult{}, err
		}
		_ = runtime.media.Stop(payload.CallID)
		return executionResult{}, nil
	case "call.hold":
		if strings.TrimSpace(payload.CallID) == "" {
			return executionResult{}, errInvalidCommand
		}
		return executionResult{}, runtime.calls.Hold(ctx)
	case "call.resume":
		if strings.TrimSpace(payload.CallID) == "" {
			return executionResult{}, errInvalidCommand
		}
		return executionResult{}, runtime.calls.Resume(ctx)
	case "call.swap":
		if strings.TrimSpace(payload.CallID) == "" {
			return executionResult{}, errInvalidCommand
		}
		return executionResult{}, runtime.calls.Swap(ctx)
	case "call.dtmf":
		digits := strings.TrimSpace(payload.Digits)
		if strings.TrimSpace(payload.CallID) == "" || digits == "" || digits != payload.Digits {
			return executionResult{}, errInvalidCommand
		}
		for _, digit := range digits {
			if !strings.ContainsRune("0123456789*#", digit) {
				return executionResult{}, errInvalidCommand
			}
			if err := runtime.calls.SendDTMF(ctx, string(digit)); err != nil {
				return executionResult{}, err
			}
		}
		return executionResult{}, nil
	default:
		return executionResult{}, errInvalidCommand
	}
}

func (runtime *Runtime) updateCallLease(payload Payload, senderInstallationID string) {
	switch payload.Operation {
	case "call.start", "call.answer":
		if payload.CallLeaseSeconds == 0 {
			return
		}
		duration := time.Duration(payload.CallLeaseSeconds) * time.Second
		runtime.activeCallLease = &callLease{
			callID: payload.CallID, senderInstallationID: senderInstallationID,
			duration: duration, expiresAt: runtime.now().Add(duration),
		}
	case "call.heartbeat", "call.media.restart", "call.hold", "call.resume", "call.swap", "call.dtmf":
		if runtime.activeCallLease != nil && runtime.activeCallLease.callID == payload.CallID &&
			runtime.activeCallLease.senderInstallationID == senderInstallationID {
			runtime.activeCallLease.expiresAt = runtime.now().Add(runtime.activeCallLease.duration)
		}
	case "call.reject", "call.end":
		if runtime.activeCallLease != nil && runtime.activeCallLease.callID == payload.CallID {
			runtime.activeCallLease = nil
		}
	}
}

func validCallLeaseSeconds(seconds int) bool {
	return seconds == 0 || (seconds >= int(defaultCallLease/time.Second) && seconds <= 120)
}

func (runtime *Runtime) expireCallLease() error {
	if runtime.activeCallLease == nil {
		return nil
	}
	if state := runtime.calls.State(); state == call.StateIdle || state == call.StateEnded {
		runtime.activeCallLease = nil
		return nil
	}
	if runtime.now().Before(runtime.activeCallLease.expiresAt) {
		return nil
	}
	runtime.logger.Warn(
		"active call lease expired; releasing modem call",
		"call_id", runtime.activeCallLease.callID,
		"sender_installation_id", runtime.activeCallLease.senderInstallationID,
	)
	return runtime.releaseActiveCall("active call lease expired")
}

func (runtime *Runtime) releaseActiveCall(reason string) error {
	lease := runtime.activeCallLease
	if lease == nil {
		return nil
	}
	cleanupContext, cancel := context.WithTimeout(context.Background(), callCleanupTimeout)
	defer cancel()
	var hangupError error
	for attempt := 1; attempt <= callCleanupAttempts; attempt++ {
		hangupError = runtime.calls.Hangup(cleanupContext)
		if hangupError == nil {
			break
		}
		runtime.logger.Warn(
			"orphaned call hangup attempt failed",
			"call_id", lease.callID,
			"reason", reason,
			"attempt", attempt,
			"max_attempts", callCleanupAttempts,
			"error", hangupError,
		)
	}
	mediaError := runtime.media.Stop(lease.callID)
	runtime.events.Unregister(lease.callID)
	runtime.activeCallLease = nil
	if hangupError == nil && mediaError == nil {
		runtime.logger.Info("orphaned call released", "call_id", lease.callID, "reason", reason)
		return nil
	}
	return fmt.Errorf("release orphaned call %s: %w", lease.callID, errors.Join(hangupError, mediaError))
}

func (runtime *Runtime) targetMatches(payload Payload) bool {
	if payload.SlotID != "" && !sameUUID(payload.SlotID, runtime.identity.SlotID) {
		return false
	}
	if payload.SIMID != "" && !sameUUID(payload.SIMID, runtime.identity.SIMID) {
		return false
	}
	return true
}

func sameUUID(left string, right string) bool {
	leftUUID, leftError := uuid.Parse(left)
	if leftError != nil {
		return false
	}
	rightUUID, rightError := uuid.Parse(right)
	return rightError == nil && leftUUID == rightUUID
}

var (
	errInvalidCommand   = errors.New("invalid command payload")
	errWrongSlot        = errors.New("command targets another SIM slot")
	errMediaUnavailable = errors.New("call media unavailable")
	errMMSUnavailable   = errors.New("modem session does not support MMS")
	errESIMUnavailable  = errors.New("modem session does not support eSIM profile management")
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
	if errors.Is(err, errMMSUnavailable) {
		return "mms_unavailable"
	}
	if errors.Is(err, errESIMUnavailable) {
		return "esim_unavailable"
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
