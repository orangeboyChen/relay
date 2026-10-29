package serverapi

import (
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	connect "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	communicationv1connect "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1/communicationv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-device/internal/callevent"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/media"
	"sim-deck-device/internal/messageevent"
	"sim-deck-device/internal/radio"
)

// Client talks to the server's device endpoints without exposing identity secrets in logs.
type Client struct {
	baseURL      string
	httpClient   *http.Client
	eventClient  eventPublisher
	deviceClient deviceRPCClient
}

type eventPublisher interface {
	PublishEvent(context.Context, *communicationv1.PublishEventRequest) (*communicationv1.PublishEventResponse, error)
}

type deviceRPCClient interface {
	FetchTurnCredentials(context.Context, *communicationv1.FetchTurnCredentialsRequest) (*communicationv1.FetchTurnCredentialsResponse, error)
	UpdateRadioStatus(context.Context, *communicationv1.UpdateRadioStatusRequest) (*communicationv1.UpdateRadioStatusResponse, error)
	UpdateSimIdentity(context.Context, *communicationv1.UpdateSimIdentityRequest) (*communicationv1.UpdateSimIdentityResponse, error)
	ListInstallationRecipients(context.Context, *communicationv1.ListInstallationRecipientsRequest) (*communicationv1.ListInstallationRecipientsResponse, error)
	UploadIncomingCall(context.Context, *communicationv1.UploadIncomingCallRequest) (*communicationv1.UploadIncomingCallResponse, error)
	ClaimCommand(context.Context, *communicationv1.ClaimCommandRequest) (*communicationv1.ClaimCommandResponse, error)
	CompleteCommand(context.Context, *communicationv1.CompleteCommandRequest) (*communicationv1.CompleteCommandResponse, error)
	UpdateCommandKey(context.Context, *communicationv1.UpdateCommandKeyRequest) (*communicationv1.UpdateCommandKeyResponse, error)
	SubmitDiscovery(context.Context, *communicationv1.SubmitDiscoveryRequest) (*communicationv1.SubmitDiscoveryResponse, error)
	GetDiscoveryStatus(context.Context, *communicationv1.GetDiscoveryStatusRequest) (*communicationv1.GetDiscoveryStatusResponse, error)
}

func (client *Client) deviceContext(ctx context.Context, credential string) context.Context {
	requestContext, callInfo := connect.NewClientContext(ctx)
	if credential != "" {
		callInfo.RequestHeader().Set("Authorization", "Bearer "+credential)
	}
	return requestContext
}

// FetchTURNCredentials obtains short-lived relay credentials for one bound device.
func (client *Client) FetchTURNCredentials(ctx context.Context, state identity.State) ([]media.ICEServer, error) {
	if !validUUID(state.DeviceUUID) || strings.TrimSpace(state.Credential) == "" {
		return nil, errors.New("bound device identity is required")
	}
	response, err := client.deviceClient.FetchTurnCredentials(client.deviceContext(ctx, state.Credential), &communicationv1.FetchTurnCredentialsRequest{DeviceUuid: state.DeviceUUID})
	if err != nil {
		return nil, fmt.Errorf("fetch TURN credentials: %w", err)
	}
	if response == nil || len(response.Urls) == 0 || response.Username == "" || response.Credential == "" || response.ExpiresAt == nil || response.ExpiresAt.AsTime().Before(time.Now().UTC()) {
		return nil, errors.New("server returned invalid TURN credentials")
	}
	return []media.ICEServer{{URLs: response.Urls, Username: response.Username, Credential: response.Credential}}, nil
}

// UpdateRadioStatus publishes the current modem registration and signal level.
func (client *Client) UpdateRadioStatus(ctx context.Context, state identity.State, status radio.Status) error {
	var signalBars *int32
	if status.SignalBars != nil {
		value := int32(*status.SignalBars)
		signalBars = &value
	}
	_, err := client.deviceClient.UpdateRadioStatus(client.deviceContext(ctx, state.Credential), &communicationv1.UpdateRadioStatusRequest{DeviceUuid: state.DeviceUUID, SlotKey: state.SlotKey, SlotIndex: int32(state.SlotIndex), RadioAccessTechnology: status.RadioAccessTechnology, SignalBars: signalBars})
	if err != nil {
		return fmt.Errorf("update radio status: %w", err)
	}
	return nil
}

func (client *Client) PublishRadioStatus(ctx context.Context, state identity.State, status radio.Status, reportedAt time.Time) error {
	publisher, err := messageevent.NewPublisher(client, state)
	if err != nil {
		return fmt.Errorf("create device status event publisher: %w", err)
	}
	err = publisher.PublishDeviceStatus(ctx, messageevent.DeviceStatus{
		RadioAccessTechnology: status.RadioAccessTechnology,
		SignalBars:            status.SignalBars,
		ReportedAt:            reportedAt,
	})
	if errors.Is(err, messageevent.ErrNoRecipients) {
		return nil
	}
	return err
}

// SIMAssignment is the current server-owned identity for a physical slot and inserted SIM.
type SIMAssignment struct {
	SlotID    string `json:"slot_id"`
	SIMID     string `json:"sim_id"`
	NumericID int    `json:"numeric_id"`
}

// UpdateSIMIdentity synchronizes the current card and refreshes server-owned slot identifiers.
func (client *Client) UpdateSIMIdentity(ctx context.Context, state identity.State) (SIMAssignment, error) {
	if state.Credential == "" || state.SIMFingerprint == "" || state.SlotKey == "" || state.SlotIndex < 1 {
		return SIMAssignment{}, errors.New("bound slot and SIM fingerprint are required")
	}
	fingerprint, err := hex.DecodeString(state.SIMFingerprint)
	if err != nil {
		return SIMAssignment{}, fmt.Errorf("decode SIM identity: %w", err)
	}
	response, err := client.deviceClient.UpdateSimIdentity(client.deviceContext(ctx, state.Credential), &communicationv1.UpdateSimIdentityRequest{DeviceUuid: state.DeviceUUID, SlotKey: state.SlotKey, SlotIndex: int32(state.SlotIndex), SimFingerprint: fingerprint})
	if err != nil {
		return SIMAssignment{}, fmt.Errorf("synchronize SIM identity: %w", err)
	}
	result := SIMAssignment{SlotID: response.SlotId, SIMID: response.SimId, NumericID: int(response.NumericId)}
	if !validUUID(result.SlotID) || !validUUID(result.SIMID) || result.NumericID < 1 || result.NumericID > 99 {
		return SIMAssignment{}, errors.New("server returned an invalid SIM assignment")
	}
	return result, nil
}

// InstallationRecipient identifies one approved iPhone encryption endpoint.
type InstallationRecipient = incomingcall.Recipient

// ListInstallationRecipients returns the approved iPhones that can decrypt device events.
func (client *Client) ListInstallationRecipients(ctx context.Context, deviceID string, credential string) ([]InstallationRecipient, error) {
	response, err := client.deviceClient.ListInstallationRecipients(client.deviceContext(ctx, credential), &communicationv1.ListInstallationRecipientsRequest{DeviceUuid: deviceID})
	if err != nil {
		return nil, fmt.Errorf("list installation recipients: %w", err)
	}
	installations := make([]InstallationRecipient, 0, len(response.Installations))
	for _, item := range response.Installations {
		recipient := InstallationRecipient{InstallationID: item.InstallationId, IdentityPublicKey: item.IdentityPublicKey, KeyAgreementPublicKey: item.KeyAgreementPublicKey}
		if !validUUID(recipient.InstallationID) || len(recipient.IdentityPublicKey) != ed25519.PublicKeySize || len(recipient.KeyAgreementPublicKey) != 32 {
			return nil, errors.New("server returned an invalid installation recipient")
		}
		installations = append(installations, recipient)
	}
	return installations, nil
}

// UploadIncomingCall queues independently encrypted VoIP deliveries for approved iPhones.
func (client *Client) UploadIncomingCall(ctx context.Context, deviceID string, credential string, notification incomingcall.Notification) error {
	if notification.DeviceUUID != deviceID {
		return errors.New("incoming call device does not match the authenticated device")
	}
	recipients := make([]*communicationv1.IncomingCallRecipient, 0, len(notification.Recipients))
	for _, item := range notification.Recipients {
		recipients = append(recipients, &communicationv1.IncomingCallRecipient{InstallationId: item.InstallationID, SenderPublicKey: item.SenderPublicKey, Nonce: item.Nonce, Ciphertext: item.Ciphertext, Signature: item.Signature})
	}
	_, err := client.deviceClient.UploadIncomingCall(client.deviceContext(ctx, credential), &communicationv1.UploadIncomingCallRequest{Notification: &communicationv1.IncomingCallNotification{Id: notification.ID, Type: notification.Type, DeviceUuid: notification.DeviceUUID, OccurredAt: timestamppb.New(notification.OccurredAt), KeyEpoch: int32(notification.KeyEpoch), Recipients: recipients}})
	if err != nil {
		return fmt.Errorf("upload incoming call: %w", err)
	}
	return nil
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

// UploadEvent sends one signed opaque event using the bound device credential.
func (client *Client) UploadEvent(ctx context.Context, deviceID string, credential string, envelope callevent.Envelope) error {
	if envelope.DeviceUUID != deviceID {
		return errors.New("event device does not match the authenticated device")
	}
	if client.eventClient == nil {
		return errors.New("encrypted event RPC client is unavailable")
	}
	requestContext, callInfo := connect.NewClientContext(ctx)
	callInfo.RequestHeader().Set("Authorization", "Bearer "+credential)
	_, err := client.eventClient.PublishEvent(requestContext, &communicationv1.PublishEventRequest{
		Event: &communicationv1.EncryptedEvent{
			Id: envelope.ID, Sequence: envelope.Sequence, Type: envelope.Type, DeviceUuid: envelope.DeviceUUID,
			RecipientInstallationId: optionalString(envelope.RecipientInstallationID), OccurredAt: timestamppb.New(envelope.OccurredAt),
			KeyEpoch: int32(envelope.KeyEpoch), Nonce: envelope.Nonce, Ciphertext: envelope.Ciphertext, Signature: envelope.Signature,
		},
	})
	if err != nil {
		return fmt.Errorf("upload encrypted event: %w", err)
	}
	return nil
}

// Claim leases the next encrypted command for an authenticated device.
func (client *Client) Claim(ctx context.Context, deviceID string, credential string) (*command.Envelope, error) {
	response, err := client.deviceClient.ClaimCommand(client.deviceContext(ctx, credential), &communicationv1.ClaimCommandRequest{DeviceUuid: deviceID})
	if err != nil {
		return nil, fmt.Errorf("claim encrypted command: %w", err)
	}
	if response == nil || !response.HasCommand || response.Command == nil {
		return nil, nil
	}
	item := response.Command
	if item.CreatedAt == nil || item.ExpiresAt == nil {
		return nil, errors.New("server returned an invalid encrypted command")
	}
	return &command.Envelope{ID: item.Id, DeviceUUID: item.DeviceUuid, SenderInstallationID: item.SenderInstallationId, SenderPublicKey: item.SenderPublicKey, Nonce: item.Nonce, Ciphertext: item.Ciphertext, Signature: item.Signature, CreatedAt: item.CreatedAt.AsTime(), ExpiresAt: item.ExpiresAt.AsTime()}, nil
}

// Complete uploads an opaque encrypted command result.
func (client *Client) Complete(ctx context.Context, deviceID string, credential string, commandID string, result command.EncryptedResult) error {
	_, err := client.deviceClient.CompleteCommand(client.deviceContext(ctx, credential), &communicationv1.CompleteCommandRequest{DeviceUuid: deviceID, CommandId: commandID, Nonce: result.Nonce, Ciphertext: result.Ciphertext})
	if err != nil {
		return fmt.Errorf("complete encrypted command: %w", err)
	}
	return nil
}

// UpdateCommandKey registers the device's current X25519 public key after migration or restore.
func (client *Client) UpdateCommandKey(ctx context.Context, state identity.State) error {
	_, err := client.deviceClient.UpdateCommandKey(client.deviceContext(ctx, state.Credential), &communicationv1.UpdateCommandKeyRequest{DeviceUuid: state.DeviceUUID, CommandPublicKey: state.CommandPublicKey()})
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnauthenticated {
			return fmt.Errorf("%w: %v", ErrDeviceCredentialInvalid, err)
		}
		return fmt.Errorf("update device command key: %w", err)
	}
	return nil
}

// DiscoveryResult is the app-controlled admission state returned by the server.
type DiscoveryResult struct {
	DeviceUUID string `json:"device_uuid"`
	Status     string `json:"status"`
	NumericID  int    `json:"numeric_id,omitempty"`
	SlotID     string `json:"slot_id,omitempty"`
	SIMID      string `json:"sim_id,omitempty"`
	SlotKey    string `json:"slot_key,omitempty"`
	SlotIndex  int    `json:"slot_index,omitempty"`
}

const (
	DiscoveryPending  = "pending"
	DiscoveryApproved = "approved"
	DiscoveryRejected = "rejected"
)

var ErrDiscoveryUnavailable = errors.New("device discovery request is unavailable")
var ErrDeviceCredentialInvalid = errors.New("device credential is invalid or revoked")

// NewClient creates a server API client with an injected transport for testing.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				ForceAttemptHTTP2: true,
				MaxIdleConns:      16,
				IdleConnTimeout:   90 * time.Second,
			},
		}
	}
	baseURL = strings.TrimRight(baseURL, "/")
	transport := connecthttp.NewTransport(httpClient, baseURL)
	return &Client{
		baseURL: baseURL, httpClient: httpClient,
		eventClient:  communicationv1connect.NewCommunicationSyncServiceClient(connect.NewClient(transport)),
		deviceClient: communicationv1connect.NewDeviceServiceClient(connect.NewClient(transport)),
	}
}

func newClientWithEventPublisher(baseURL string, httpClient *http.Client, publisher eventPublisher) *Client {
	client := NewClient(baseURL, httpClient)
	client.eventClient = publisher
	return client
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// SubmitDiscovery asks the active app-controlled window to display this device for approval.
func (client *Client) SubmitDiscovery(ctx context.Context, pendingCredential string, state identity.State) (DiscoveryResult, error) {
	publicKey := state.PublicKey()
	commandPublicKey := state.CommandPublicKey()
	if strings.TrimSpace(pendingCredential) == "" || !validUUID(state.DeviceUUID) ||
		len(publicKey) != ed25519.PublicKeySize || len(commandPublicKey) != 32 ||
		len(state.IdentityPrivateKey) != ed25519.PrivateKeySize {
		return DiscoveryResult{}, errors.New("pending credential and valid device identity are required")
	}
	proof := discoverySignedData(state.DeviceUUID, publicKey, commandPublicKey, pendingCredential)
	if state.SlotIndex > 0 || state.SIMFingerprint != "" {
		proof = discoverySignedDataWithSlot(state.DeviceUUID, publicKey, commandPublicKey, pendingCredential, state.SlotKey, state.SlotIndex, state.SIMFingerprint)
	}
	fingerprint, err := hex.DecodeString(state.SIMFingerprint)
	if err != nil && state.SIMFingerprint != "" {
		return DiscoveryResult{}, fmt.Errorf("decode SIM fingerprint: %w", err)
	}
	response, err := client.deviceClient.SubmitDiscovery(ctx, &communicationv1.SubmitDiscoveryRequest{
		DeviceUuid: state.DeviceUUID, SlotKey: state.SlotKey, SlotIndex: int32(state.SlotIndex), SimFingerprint: hex.EncodeToString(fingerprint),
		IdentityPublicKey: publicKey, CommandPublicKey: commandPublicKey, PendingCredential: pendingCredential,
		Signature: ed25519.Sign(state.IdentityPrivateKey, proof),
	})
	if err != nil {
		if connect.CodeOf(err) == connect.CodeAlreadyExists && strings.Contains(strings.ToLower(err.Error()), "device discovery is not active") {
			return DiscoveryResult{}, ErrDiscoveryUnavailable
		}
		return DiscoveryResult{}, fmt.Errorf("submit device discovery: %w", err)
	}
	return discoveryResultFromProto(response.DeviceUuid, response.Status, response.NumericId, response.SlotId, response.SimId, response.SlotKey, int(response.SlotIndex), state.DeviceUUID)
}

// GetDiscoveryStatus polls the approval result with the private pending credential.
func (client *Client) GetDiscoveryStatus(ctx context.Context, deviceID string, pendingCredential string) (DiscoveryResult, error) {
	if !validUUID(deviceID) || strings.TrimSpace(pendingCredential) == "" {
		return DiscoveryResult{}, errors.New("device UUID and pending credential are required")
	}
	response, err := client.deviceClient.GetDiscoveryStatus(client.deviceContext(ctx, pendingCredential), &communicationv1.GetDiscoveryStatusRequest{DeviceUuid: deviceID})
	if err != nil {
		if connect.CodeOf(err) == connect.CodeUnauthenticated || connect.CodeOf(err) == connect.CodeNotFound {
			return DiscoveryResult{}, ErrDiscoveryUnavailable
		}
		return DiscoveryResult{}, fmt.Errorf("get device discovery status: %w", err)
	}
	return discoveryResultFromProto(response.DeviceUuid, response.Status, response.NumericId, response.SlotId, response.SimId, response.SlotKey, int(response.SlotIndex), deviceID)
}

func discoveryResultFromProto(deviceID, status string, numericID int32, slotID, simID *string, slotKey string, slotIndex int, expectedDeviceID string) (DiscoveryResult, error) {
	result := DiscoveryResult{DeviceUUID: deviceID, Status: status, NumericID: int(numericID), SlotID: optionalStringValue(slotID), SIMID: optionalStringValue(simID), SlotKey: slotKey, SlotIndex: slotIndex}
	if result.DeviceUUID != expectedDeviceID {
		return DiscoveryResult{}, errors.New("server returned an invalid device discovery result for a different device")
	}
	switch result.Status {
	case DiscoveryPending, DiscoveryRejected:
		if result.NumericID != 0 {
			return DiscoveryResult{}, errors.New("server returned an invalid device discovery result")
		}
	case DiscoveryApproved:
		if result.NumericID < 1 || result.NumericID > 99 {
			return DiscoveryResult{}, errors.New("server returned an invalid device discovery result")
		}
		if (result.SlotID != "" && !validUUID(result.SlotID)) || (result.SIMID != "" && !validUUID(result.SIMID)) {
			return DiscoveryResult{}, errors.New("server returned invalid slot or SIM identity")
		}
	default:
		return DiscoveryResult{}, errors.New("server returned an invalid device discovery result")
	}
	return result, nil
}

func optionalStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func discoverySignedData(deviceUUID string, identityPublicKey []byte, commandPublicKey []byte, credential string) []byte {
	fields := [][]byte{[]byte("sim-deck-device-discovery-v1"), []byte(deviceUUID), identityPublicKey, commandPublicKey, []byte(credential)}
	return encodeDiscoveryFields(fields)
}

func discoverySignedDataWithSlot(deviceUUID string, identityPublicKey []byte, commandPublicKey []byte, credential string, slotKey string, slotIndex int, simFingerprint string) []byte {
	fields := [][]byte{
		[]byte("sim-deck-device-discovery-v1"), []byte(deviceUUID),
		identityPublicKey, commandPublicKey, []byte(credential), []byte(slotKey), []byte(strconv.Itoa(slotIndex)), []byte(simFingerprint),
	}
	return encodeDiscoveryFields(fields)
}

func encodeDiscoveryFields(fields [][]byte) []byte {
	data := make([]byte, 0, 128)
	for _, field := range fields {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		data = append(data, length[:]...)
		data = append(data, field...)
	}
	return data
}
