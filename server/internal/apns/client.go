package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultProductionURL    = "https://api.push.apple.com"
	defaultSandboxURL       = "https://api.sandbox.push.apple.com"
	maximumAlertPayloadSize = 4 * 1024
	maximumVoIPPayloadSize  = 5 * 1024
	providerTokenTTL        = 50 * time.Minute
	alertExpiration         = 24 * time.Hour
	genericAlertPayload     = `{"aps":{"alert":{"title":"Sim Deck","body":"New message"},"content-available":1,"mutable-content":1}}`
)

var (
	ErrInvalidConfiguration = errors.New("invalid APNs configuration")
	ErrInvalidPayload       = errors.New("invalid APNs payload")
)

// Config contains the provider-token credentials used to reach APNs.
type Config struct {
	KeyID         string
	TeamID        string
	Topic         string
	PrivateKeyPEM []byte
	HTTPClient    *http.Client
	ProductionURL string
	SandboxURL    string
	Now           func() time.Time
}

// Client sends opaque encrypted deliveries through APNs.
type Client struct {
	keyID         string
	teamID        string
	topic         string
	privateKey    *ecdsa.PrivateKey
	httpClient    *http.Client
	productionURL string
	sandboxURL    string
	now           func() time.Time

	mu             sync.Mutex
	providerToken  string
	tokenExpiresAt time.Time
}

// ResponseError describes a non-success APNs response.
type ResponseError struct {
	StatusCode int
	Reason     string
}

func (failure *ResponseError) Error() string {
	if failure.Reason == "" {
		return fmt.Sprintf("APNs returned HTTP %d", failure.StatusCode)
	}
	return fmt.Sprintf("APNs returned HTTP %d: %s", failure.StatusCode, failure.Reason)
}

// Retryable reports whether APNs recommends attempting the delivery later.
func (failure *ResponseError) Retryable() bool {
	return failure.StatusCode == http.StatusTooManyRequests || failure.StatusCode >= http.StatusInternalServerError
}

// InvalidatesToken reports whether the device token must no longer be used.
func (failure *ResponseError) InvalidatesToken() bool {
	return failure.StatusCode == http.StatusGone ||
		failure.Reason == "BadDeviceToken" ||
		failure.Reason == "DeviceTokenNotForTopic" ||
		failure.Reason == "Unregistered"
}

// New creates an APNs client using an ES256 provider signing key.
func New(config Config) (*Client, error) {
	if strings.TrimSpace(config.KeyID) == "" || strings.TrimSpace(config.TeamID) == "" || strings.TrimSpace(config.Topic) == "" {
		return nil, ErrInvalidConfiguration
	}
	privateKey, err := parsePrivateKey(config.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	productionURL := strings.TrimRight(config.ProductionURL, "/")
	if productionURL == "" {
		productionURL = defaultProductionURL
	}
	sandboxURL := strings.TrimRight(config.SandboxURL, "/")
	if sandboxURL == "" {
		sandboxURL = defaultSandboxURL
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		keyID:         config.KeyID,
		teamID:        config.TeamID,
		topic:         config.Topic,
		privateKey:    privateKey,
		httpClient:    httpClient,
		productionURL: productionURL,
		sandboxURL:    sandboxURL,
		now:           now,
	}, nil
}

// SendVoIP sends one encrypted VoIP delivery to the selected APNs environment.
func (client *Client) SendVoIP(ctx context.Context, environment string, deviceToken []byte, encryptedDelivery []byte) error {
	return client.send(ctx, environment, deviceToken, encryptedDelivery, "voip", 10)
}

// SendAlert sends an encrypted background alert delivery to the notification service extension.
func (client *Client) SendAlert(ctx context.Context, environment string, deviceToken []byte, encryptedDelivery []byte) error {
	return client.send(ctx, environment, deviceToken, encryptedDelivery, "alert", 10)
}

func (client *Client) send(ctx context.Context, environment string, deviceToken []byte, encryptedDelivery []byte, pushType string, priority int) error {
	baseURL := client.productionURL
	if environment == "sandbox" {
		baseURL = client.sandboxURL
	} else if environment != "production" {
		return ErrInvalidConfiguration
	}
	if len(deviceToken) == 0 {
		return ErrInvalidConfiguration
	}
	payload, err := alertPayload(encryptedDelivery, pushType == "voip")
	if err != nil {
		return err
	}
	token, err := client.authorizationToken()
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		baseURL+"/3/device/"+hex.EncodeToString(deviceToken),
		strings.NewReader(string(payload)),
	)
	if err != nil {
		return fmt.Errorf("create APNs request: %w", err)
	}
	request.Header.Set("authorization", "bearer "+token)
	topic := client.topic
	if pushType == "alert" {
		topic = strings.TrimSuffix(topic, ".voip")
	}
	request.Header.Set("apns-topic", topic)
	request.Header.Set("apns-push-type", pushType)
	request.Header.Set("apns-priority", fmt.Sprint(priority))
	expiration := int64(0)
	if pushType == "alert" {
		expiration = client.now().UTC().Add(alertExpiration).Unix()
	}
	request.Header.Set("apns-expiration", fmt.Sprint(expiration))
	request.Header.Set("content-type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("send APNs request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	var body struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(io.LimitReader(response.Body, 4*1024)).Decode(&body)
	return &ResponseError{StatusCode: response.StatusCode, Reason: body.Reason}
}

func parsePrivateKey(encoded []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(encoded)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, ErrInvalidConfiguration
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse APNs private key: %w", err)
	}
	privateKey, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || privateKey.Curve != elliptic.P256() {
		return nil, ErrInvalidConfiguration
	}
	return privateKey, nil
}

func alertPayload(encryptedDelivery []byte, voip bool) ([]byte, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encryptedDelivery, &payload); err != nil || payload == nil {
		return nil, ErrInvalidPayload
	}
	if _, exists := payload["aps"]; exists {
		return nil, ErrInvalidPayload
	}
	if voip {
		payload["aps"] = json.RawMessage(`{"content-available":1}`)
	} else {
		payload["aps"] = json.RawMessage(`{"alert":{"title":"Sim Deck","body":"New message"},"content-available":1,"mutable-content":1}`)
	}
	encoded, err := json.Marshal(payload)
	maximumPayloadSize := maximumAlertPayloadSize
	if voip {
		maximumPayloadSize = maximumVoIPPayloadSize
	}
	if err != nil {
		return nil, ErrInvalidPayload
	}
	if len(encoded) > maximumPayloadSize {
		if !voip {
			return []byte(genericAlertPayload), nil
		}
		return nil, ErrInvalidPayload
	}
	return encoded, nil
}

func (client *Client) authorizationToken() (string, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	now := client.now().UTC()
	if client.providerToken != "" && now.Before(client.tokenExpiresAt) {
		return client.providerToken, nil
	}
	header, err := json.Marshal(map[string]string{"alg": "ES256", "kid": client.keyID})
	if err != nil {
		return "", fmt.Errorf("encode APNs token header: %w", err)
	}
	claims, err := json.Marshal(map[string]any{"iss": client.teamID, "iat": now.Unix()})
	if err != nil {
		return "", fmt.Errorf("encode APNs token claims: %w", err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, client.privateKey, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign APNs provider token: %w", err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	client.providerToken = unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	client.tokenExpiresAt = now.Add(providerTokenTTL)
	return client.providerToken, nil
}
