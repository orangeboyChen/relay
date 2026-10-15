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
	"strings"
	"time"

	"sim-deck-device/internal/identity"
)

const maxResponseBytes = 1 << 20

// Client talks to the server's device endpoints without exposing identity secrets in logs.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// PairingResult contains the durable binding returned exactly once by the server.
type PairingResult struct {
	Credential string
	NumericID  int
}

// NewClient creates a server API client with an injected transport for testing.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient}
}

// Pair consumes a one-time token and registers the device identity public key.
func (client *Client) Pair(ctx context.Context, pairingToken string, state identity.State) (PairingResult, error) {
	publicKey := state.PublicKey()
	if strings.TrimSpace(pairingToken) == "" || len(publicKey) != ed25519.PublicKeySize {
		return PairingResult{}, errors.New("pairing token and valid device identity are required")
	}
	body, err := json.Marshal(map[string]any{
		"pairing_token":       pairingToken,
		"device_uuid":         state.DeviceUUID,
		"profile_ciphertext":  []byte{},
		"identity_public_key": publicKey,
		"wrapped_vault_key":   []byte{},
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
		Credential string `json:"credential"`
	}
	if err := json.NewDecoder(limitedBody).Decode(&result); err != nil {
		return PairingResult{}, fmt.Errorf("decode pairing response: %w", err)
	}
	if result.Device.DeviceUUID != state.DeviceUUID || result.Device.NumericID < 1 || result.Device.NumericID > 99 || strings.TrimSpace(result.Credential) == "" {
		return PairingResult{}, errors.New("pair device: server returned an invalid binding")
	}
	return PairingResult{Credential: result.Credential, NumericID: result.Device.NumericID}, nil
}
