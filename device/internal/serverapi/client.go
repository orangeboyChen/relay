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
)

const maxResponseBytes = 1 << 20

// Client talks to the server's device endpoints without exposing identity secrets in logs.
type Client struct {
	baseURL    string
	httpClient *http.Client
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
