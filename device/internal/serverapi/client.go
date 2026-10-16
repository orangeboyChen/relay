package serverapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sim-deck-device/internal/callevent"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/radio"
)

const maxResponseBytes = 1 << 20

// Client talks to the server's device endpoints without exposing identity secrets in logs.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// UpdateRadioStatus publishes the current modem registration and signal level.
func (client *Client) UpdateRadioStatus(ctx context.Context, state identity.State, status radio.Status) error {
	body, err := json.Marshal(map[string]any{
		"device_uuid":             state.DeviceUUID,
		"radio_access_technology": status.RadioAccessTechnology,
		"signal_bars":             status.SignalBars,
	})
	if err != nil {
		return fmt.Errorf("encode radio status: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, client.baseURL+"/v1/device-radio-status", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create radio status request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+state.Credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("update radio status: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError("update radio status", response.StatusCode, io.LimitReader(response.Body, maxResponseBytes))
	}
	return nil
}

// InstallationRecipient identifies one approved iPhone encryption endpoint.
type InstallationRecipient = incomingcall.Recipient

// ListInstallationRecipients returns the approved iPhones that can decrypt device events.
func (client *Client) ListInstallationRecipients(ctx context.Context, deviceID string, credential string) ([]InstallationRecipient, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		client.baseURL+"/v1/device-installations?device_uuid="+url.QueryEscape(deviceID),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create installation recipient request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list installation recipients: %w", err)
	}
	defer response.Body.Close()

	limitedBody := io.LimitReader(response.Body, maxResponseBytes)
	if response.StatusCode != http.StatusOK {
		return nil, responseError("list installation recipients", response.StatusCode, limitedBody)
	}
	var body struct {
		Installations []InstallationRecipient `json:"installations"`
	}
	if err := json.NewDecoder(limitedBody).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode installation recipients: %w", err)
	}
	for _, recipient := range body.Installations {
		if !validUUID(recipient.InstallationID) || len(recipient.KeyAgreementPublicKey) != 32 {
			return nil, errors.New("server returned an invalid installation recipient")
		}
	}
	if body.Installations == nil {
		return []InstallationRecipient{}, nil
	}
	return body.Installations, nil
}

// UploadIncomingCall queues independently encrypted VoIP deliveries for approved iPhones.
func (client *Client) UploadIncomingCall(ctx context.Context, deviceID string, credential string, notification incomingcall.Notification) error {
	if notification.DeviceUUID != deviceID {
		return errors.New("incoming call device does not match the authenticated device")
	}
	body, err := json.Marshal(map[string]any{"notification": notification})
	if err != nil {
		return fmt.Errorf("encode incoming call notification: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/incoming-calls", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create incoming call request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("upload incoming call: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return responseError("upload incoming call", response.StatusCode, io.LimitReader(response.Body, maxResponseBytes))
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
	body, err := json.Marshal(map[string]any{"envelope": envelope})
	if err != nil {
		return fmt.Errorf("encode encrypted event: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create encrypted event request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("upload encrypted event: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return responseError("upload encrypted event", response.StatusCode, io.LimitReader(response.Body, maxResponseBytes))
	}
	return nil
}

// Claim leases the next encrypted command for an authenticated device.
func (client *Client) Claim(ctx context.Context, deviceID string, credential string) (*command.Envelope, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		client.baseURL+"/v1/device-commands/next?device_uuid="+url.QueryEscape(deviceID),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create command claim request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("claim encrypted command: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	limitedBody := io.LimitReader(response.Body, maxResponseBytes)
	if response.StatusCode != http.StatusOK {
		return nil, responseError("claim encrypted command", response.StatusCode, limitedBody)
	}
	var body struct {
		Command command.Envelope `json:"command"`
	}
	if err := json.NewDecoder(limitedBody).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode encrypted command: %w", err)
	}
	return &body.Command, nil
}

// Complete uploads an opaque encrypted command result.
func (client *Client) Complete(ctx context.Context, deviceID string, credential string, commandID string, result command.EncryptedResult) error {
	body, err := json.Marshal(map[string]any{"device_uuid": deviceID, "result": result})
	if err != nil {
		return fmt.Errorf("encode command completion: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/device-commands/"+url.PathEscape(commandID)+"/complete", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create command completion request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("complete encrypted command: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError("complete encrypted command", response.StatusCode, io.LimitReader(response.Body, maxResponseBytes))
	}
	return nil
}

// UpdateCommandKey registers the device's current X25519 public key after migration or restore.
func (client *Client) UpdateCommandKey(ctx context.Context, state identity.State) error {
	body, err := json.Marshal(map[string]any{
		"device_uuid":        state.DeviceUUID,
		"command_public_key": state.CommandPublicKey(),
	})
	if err != nil {
		return fmt.Errorf("encode command key update: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, client.baseURL+"/v1/device-command-key", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create command key update request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+state.Credential)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("update device command key: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return responseError("update device command key", response.StatusCode, io.LimitReader(response.Body, maxResponseBytes))
	}
	return nil
}

func responseError(operation string, statusCode int, body io.Reader) error {
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(body).Decode(&failure)
	if failure.Error.Code == "" {
		failure.Error.Code = "unexpected_response"
	}
	return fmt.Errorf("%s: server returned %d (%s)", operation, statusCode, failure.Error.Code)
}

// PairingResult contains the durable binding returned exactly once by the server.
type PairingResult struct {
	NumericID int
}

// NewClient creates a server API client with an injected transport for testing.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

// Pair consumes a one-time token and registers the device identity public key.
func (client *Client) Pair(ctx context.Context, pairingToken string, deviceCredential string, state identity.State) (PairingResult, error) {
	publicKey := state.PublicKey()
	if strings.TrimSpace(pairingToken) == "" || strings.TrimSpace(deviceCredential) == "" || len(publicKey) != ed25519.PublicKeySize || len(state.CommandPublicKey()) != 32 {
		return PairingResult{}, errors.New("pairing token, device credential, and valid device identity are required")
	}
	body, err := json.Marshal(map[string]any{
		"pairing_token":       pairingToken,
		"device_uuid":         state.DeviceUUID,
		"profile_ciphertext":  []byte{},
		"identity_public_key": publicKey,
		"command_public_key":  state.CommandPublicKey(),
		"wrapped_vault_key":   []byte{},
		"device_credential":   deviceCredential,
	})
	if err != nil {
		return PairingResult{}, fmt.Errorf("encode pairing request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/v1/device-pairings", bytes.NewReader(body))
	if err != nil {
		return PairingResult{}, fmt.Errorf("create pairing request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return PairingResult{}, fmt.Errorf("pair device: %w", err)
	}
	defer response.Body.Close()

	limitedBody := io.LimitReader(response.Body, maxResponseBytes)
	if response.StatusCode != http.StatusCreated {
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(limitedBody).Decode(&failure)
		if failure.Error.Code == "" {
			failure.Error.Code = "unexpected_response"
		}
		return PairingResult{}, fmt.Errorf("pair device: server returned %d (%s)", response.StatusCode, failure.Error.Code)
	}
	var result struct {
		Device struct {
			DeviceUUID string `json:"device_uuid"`
			NumericID  int    `json:"numeric_id"`
		} `json:"device"`
	}
	if err := json.NewDecoder(limitedBody).Decode(&result); err != nil {
		return PairingResult{}, fmt.Errorf("decode pairing response: %w", err)
	}
	if result.Device.DeviceUUID != state.DeviceUUID || result.Device.NumericID < 1 || result.Device.NumericID > 99 {
		return PairingResult{}, errors.New("pair device: server returned an invalid binding")
	}
	return PairingResult{NumericID: result.Device.NumericID}, nil
}
