package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
	"sim-deck-server/internal/store"
)

// Repository defines the encrypted persistence operations exposed by the API.
type Repository interface {
	ListDevices(context.Context) ([]store.DeviceRecord, error)
	UpdateNumericID(context.Context, string, int) error
	UpdateDeviceProfile(context.Context, string, []byte) error
	RevokeDevice(context.Context, string, time.Time) error
	InsertEvent(context.Context, domain.EventEnvelope, time.Time) (int64, bool, error)
	ListEvents(context.Context, int64, int, time.Time) ([]domain.EventEnvelope, error)
	OwnerCredentialByUsername(context.Context, string) (store.OwnerCredential, error)
	CreateSession(context.Context, string, auth.TokenPair) error
	RotateSession(context.Context, string, auth.TokenPair, time.Time) error
	RevokeSession(context.Context, string, time.Time) error
	AuthenticateOwnerSession(context.Context, string, time.Time) (string, error)
	CreatePairingToken(context.Context, string, []byte, time.Time) error
	ConsumePairingToken(context.Context, []byte, time.Time, store.DevicePairing) (store.DeviceRecord, error)
	OpenDeviceDiscovery(context.Context, string, time.Time, time.Time) (store.DeviceDiscoverySession, error)
	CloseDeviceDiscovery(context.Context, string, time.Time) error
	GetDeviceDiscoverySession(context.Context, string) (store.DeviceDiscoverySession, error)
	SubmitDeviceDiscovery(context.Context, time.Time, store.DeviceDiscoveryRequest) (store.DeviceDiscoveryRecord, error)
	ListPendingDeviceDiscoveries(context.Context, string, time.Time) ([]store.DeviceDiscoveryRecord, error)
	ApproveDeviceDiscovery(context.Context, string, string, time.Time) (store.DeviceRecord, error)
	RejectDeviceDiscovery(context.Context, string, string, time.Time) error
	DeviceDiscoveryStatus(context.Context, string, []byte, time.Time) (store.DeviceDiscoveryRecord, error)
	AuthenticateDevice(context.Context, string, string, time.Time) ([]byte, error)
	EnqueueCommand(context.Context, domain.EncryptedCommand) (bool, error)
	ClaimCommand(context.Context, string, time.Time, time.Time) (domain.EncryptedCommand, error)
	CompleteCommand(context.Context, string, string, domain.EncryptedCommandResult, time.Time) error
	CommandStatus(context.Context, string, string, time.Time) (domain.CommandStatus, error)
	UpdateDeviceCommandPublicKey(context.Context, string, []byte) error
	UpdateDeviceRadioStatus(context.Context, string, string, int, string, *int, time.Time) error
	UpdateDeviceSIMIdentity(context.Context, string, string, int, []byte, time.Time) (string, int, error)
	RegisterAppInstallation(context.Context, string, store.AppInstallationRegistration, time.Time) (store.AppInstallationRecord, bool, error)
	UpsertPushToken(context.Context, string, string, string, string, []byte, time.Time) error
	DeletePushToken(context.Context, string, string, string, string) error
	ListApprovedAppInstallationRecipients(context.Context) ([]store.AppInstallationRecipient, error)
	InsertIncomingCall(context.Context, domain.IncomingCallNotification, time.Time) (bool, error)
	ClaimIncomingCall(context.Context, string, string, string, time.Time) (domain.IncomingCallClaim, error)
}

// Config describes public endpoints advertised to clients.
type Config struct {
	PublicBaseURL string
	MediaUDPPort  int
	MediaTCPPort  int
	Now           func() time.Time
}

// Handler builds the versioned HTTP API.
func Handler(repository Repository, config Config) http.Handler {
	router := &router{repository: repository, config: config}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", router.health)
	mux.HandleFunc("GET /.well-known/sim-deck", router.discovery)
	mux.HandleFunc("GET /v1/devices", router.requireOwner(router.listDevices))
	mux.HandleFunc("PATCH /v1/devices/{deviceID}/numeric-id", router.requireOwner(router.updateNumericID))
	mux.HandleFunc("PUT /v1/devices/{deviceID}/profile", router.requireOwner(router.updateDeviceProfile))
	mux.HandleFunc("DELETE /v1/devices/{deviceID}", router.requireOwner(router.revokeDevice))
	mux.HandleFunc("POST /v1/device-pairing-tokens", router.requireOwner(router.createPairingToken))
	mux.HandleFunc("POST /v1/device-discovery-session", router.requireOwner(router.openDeviceDiscovery))
	mux.HandleFunc("GET /v1/device-discovery-session", router.requireOwner(router.getDeviceDiscoverySession))
	mux.HandleFunc("DELETE /v1/device-discovery-session", router.requireOwner(router.closeDeviceDiscovery))
	mux.HandleFunc("GET /v1/device-discoveries", router.requireOwner(router.listDeviceDiscoveries))
	mux.HandleFunc("POST /v1/device-discoveries/{deviceID}/approve", router.requireOwner(router.approveDeviceDiscovery))
	mux.HandleFunc("DELETE /v1/device-discoveries/{deviceID}", router.requireOwner(router.rejectDeviceDiscovery))
	mux.HandleFunc("POST /v1/app-installations", router.requireOwner(router.registerAppInstallation))
	mux.HandleFunc("PUT /v1/app-installations/{installationID}/push-tokens/{kind}", router.requireOwner(router.upsertPushToken))
	mux.HandleFunc("DELETE /v1/app-installations/{installationID}/push-tokens/{kind}", router.requireOwner(router.deletePushToken))
	mux.HandleFunc("GET /v1/device-installations", router.listDeviceInstallationRecipients)
	mux.HandleFunc("POST /v1/incoming-calls", router.insertIncomingCall)
	mux.HandleFunc("POST /v1/incoming-calls/{notificationID}/claim", router.requireOwner(router.claimIncomingCall))
	mux.HandleFunc("POST /v1/device-pairings", router.pairDevice)
	mux.HandleFunc("POST /v1/device-discoveries", router.submitDeviceDiscovery)
	mux.HandleFunc("GET /v1/device-discoveries/{deviceID}/status", router.deviceDiscoveryStatus)
	mux.HandleFunc("POST /v1/events", router.insertEvent)
	mux.HandleFunc("GET /v1/events", router.requireOwner(router.listEvents))
	mux.HandleFunc("GET /v1/events/ws", router.requireOwner(router.streamEvents))
	mux.HandleFunc("POST /v1/devices/{deviceID}/commands", router.requireOwner(router.enqueueCommand))
	mux.HandleFunc("GET /v1/devices/{deviceID}/commands/{commandID}", router.requireOwner(router.commandStatus))
	mux.HandleFunc("GET /v1/device-commands/next", router.claimCommand)
	mux.HandleFunc("POST /v1/device-commands/{commandID}/complete", router.completeCommand)
	mux.HandleFunc("PUT /v1/device-command-key", router.updateDeviceCommandKey)
	mux.HandleFunc("PUT /v1/device-radio-status", router.updateDeviceRadioStatus)
	mux.HandleFunc("PUT /v1/device-sim-identity", router.updateDeviceSIMIdentity)
	mux.HandleFunc("POST /v1/auth/bootstrap", router.bootstrapLogin)
	mux.HandleFunc("POST /v1/auth/refresh", router.refreshSession)
	mux.HandleFunc("POST /v1/auth/logout", router.logout)
	return withJSONContentType(mux)
}

type router struct {
	repository Repository
	config     Config
}

type ownerContextKey struct{}

const (
	pairingTokenLifetime       = 10 * time.Minute
	deviceDiscoveryLifetime    = 10 * time.Minute
	maxProfileCiphertextBytes  = 64 << 10
	maxWrappedVaultKeyBytes    = 16 << 10
	maxPushTokenBytes          = 256
	installationPublicKeyBytes = 32
	eventRetention             = 365 * 24 * time.Hour
	maxEventFutureSkew         = 5 * time.Minute
	commandLease               = 30 * time.Second
	maxCommandLifetime         = 5 * time.Minute
	commandPublicKeyBytes      = 32
	commandNonceBytes          = 12
	commandSignatureBytes      = ed25519.SignatureSize
	maxCommandCiphertextBytes  = 64 << 10
	defaultEventPageSize       = 100
	maxEventPageSize           = 500
)

func (router *router) upsertPushToken(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	installationID := request.PathValue("installationID")
	kind := request.PathValue("kind")
	var body struct {
		Environment string `json:"environment"`
		Token       []byte `json:"token"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(installationID) || (kind != "alert" && kind != "voip") ||
		(body.Environment != "sandbox" && body.Environment != "production") ||
		len(body.Token) == 0 || len(body.Token) > maxPushTokenBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_push_token", "Push token registration is invalid.")
		return
	}
	if err := router.repository.UpsertPushToken(
		request.Context(), ownerID, installationID, body.Environment, kind, body.Token, router.now(),
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "installation_unavailable", "The app installation is not approved.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to register the push token.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) deletePushToken(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	installationID := request.PathValue("installationID")
	kind := request.PathValue("kind")
	environment := request.URL.Query().Get("environment")
	if !validUUID(installationID) || (kind != "alert" && kind != "voip") ||
		(environment != "sandbox" && environment != "production") {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_push_token", "Push token route is invalid.")
		return
	}
	if err := router.repository.DeletePushToken(
		request.Context(), ownerID, installationID, environment, kind,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "push_token_unavailable", "The push token route does not exist.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to delete the push token.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) registerAppInstallation(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	var body struct {
		InstallationID        string `json:"installation_id"`
		IdentityPublicKey     []byte `json:"identity_public_key"`
		KeyAgreementPublicKey []byte `json:"key_agreement_public_key"`
		WrappedVaultKey       []byte `json:"wrapped_vault_key"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(body.InstallationID) ||
		len(body.IdentityPublicKey) != installationPublicKeyBytes ||
		len(body.KeyAgreementPublicKey) != installationPublicKeyBytes ||
		len(body.WrappedVaultKey) > maxWrappedVaultKeyBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_installation", "Installation identity or wrapped vault key is invalid.")
		return
	}
	record, created, err := router.repository.RegisterAppInstallation(
		request.Context(),
		ownerID,
		store.AppInstallationRegistration{
			ID:                    body.InstallationID,
			IdentityPublicKey:     body.IdentityPublicKey,
			KeyAgreementPublicKey: body.KeyAgreementPublicKey,
			WrappedVaultKey:       body.WrappedVaultKey,
		},
		router.now(),
	)
	if err != nil {
		if errors.Is(err, domain.ErrInstallationConflict) {
			writeError(writer, http.StatusConflict, "installation_conflict", err.Error())
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to register the app installation.")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"installation": record, "created": created})
}

func (router *router) listDeviceInstallationRecipients(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.URL.Query().Get("device_uuid")
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(deviceID) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), deviceID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	recipients, err := router.repository.ListApprovedAppInstallationRecipients(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to list trusted app installations.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"installations": recipients})
}

func (router *router) insertIncomingCall(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Notification domain.IncomingCallNotification `json:"notification"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	publicKey, err := router.repository.AuthenticateDevice(request.Context(), body.Notification.DeviceUUID, token, router.now())
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	if err := e2ee.VerifyIncomingCallNotification(ed25519.PublicKey(publicKey), body.Notification); err != nil {
		if errors.Is(err, e2ee.ErrInvalidIncomingCall) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_incoming_call", err.Error())
			return
		}
		writeError(writer, http.StatusUnauthorized, "invalid_incoming_call_signature", "Incoming call signature is invalid.")
		return
	}
	now := router.now()
	if body.Notification.OccurredAt.After(now.Add(maxEventFutureSkew)) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_incoming_call_time", "Incoming call timestamp is too far in the future.")
		return
	}
	expiresAt := body.Notification.OccurredAt.Add(eventRetention)
	if !expiresAt.After(now) {
		writeError(writer, http.StatusUnprocessableEntity, "incoming_call_expired", "Incoming call is outside the retention window.")
		return
	}
	created, err := router.repository.InsertIncomingCall(request.Context(), body.Notification, expiresAt)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrIdempotencyConflict):
			writeError(writer, http.StatusConflict, "idempotency_conflict", err.Error())
		case errors.Is(err, domain.ErrUntrustedInstallationRecipient):
			writeError(writer, http.StatusConflict, "untrusted_installation_recipient", err.Error())
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to store the incoming call.")
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]bool{"created": created})
}

func (router *router) claimIncomingCall(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	notificationID := request.PathValue("notificationID")
	var body struct {
		InstallationID string `json:"installation_id"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(notificationID) || !validUUID(body.InstallationID) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_incoming_call_claim", "Notification and installation identities are invalid.")
		return
	}
	claim, err := router.repository.ClaimIncomingCall(
		request.Context(), ownerID, notificationID, body.InstallationID, router.now(),
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "incoming_call_unavailable", "Incoming call is unavailable to this installation.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to claim the incoming call.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"claim": claim})
}

func (router *router) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (router *router) discovery(writer http.ResponseWriter, _ *http.Request) {
	baseURL := strings.TrimRight(router.config.PublicBaseURL, "/")
	writeJSON(writer, http.StatusOK, map[string]any{
		"api_version":   "v1",
		"websocket_url": websocketBaseURL(baseURL) + "/v1/events/ws",
		"media": map[string]int{
			"udp_port": router.config.MediaUDPPort,
			"tcp_port": router.config.MediaTCPPort,
		},
	})
}

func websocketBaseURL(baseURL string) string {
	switch {
	case strings.HasPrefix(baseURL, "https://"):
		return "wss://" + strings.TrimPrefix(baseURL, "https://")
	case strings.HasPrefix(baseURL, "http://"):
		return "ws://" + strings.TrimPrefix(baseURL, "http://")
	default:
		return baseURL
	}
}

// ParsePublicBaseURL validates and normalizes the externally reachable HTTP API URL.
func ParsePublicBaseURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("PUBLIC_BASE_URL is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid PUBLIC_BASE_URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("PUBLIC_BASE_URL must be an absolute HTTP or HTTPS URL without credentials, a query, or a fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = strings.TrimRight(parsed.RawPath, "/")
	return parsed.String(), nil
}

func (router *router) listDevices(writer http.ResponseWriter, request *http.Request) {
	devices, err := router.repository.ListDevices(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to list devices.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"devices": devices})
}

func (router *router) updateNumericID(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		NumericID int `json:"numeric_id"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := router.repository.UpdateNumericID(request.Context(), request.PathValue("deviceID"), body.NumericID); err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidNumericID):
			writeError(writer, http.StatusUnprocessableEntity, "invalid_numeric_id", err.Error())
		case errors.Is(err, domain.ErrDuplicateNumericID):
			writeError(writer, http.StatusConflict, "duplicate_numeric_id", err.Error())
		case errors.Is(err, pgx.ErrNoRows):
			writeError(writer, http.StatusNotFound, "device_not_found", "Device not found.")
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update the device.")
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) updateDeviceProfile(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ProfileCiphertext []byte `json:"profile_ciphertext"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(request.PathValue("deviceID")) || len(body.ProfileCiphertext) == 0 || len(body.ProfileCiphertext) > maxProfileCiphertextBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device_profile", "Encrypted device profile is invalid.")
		return
	}
	if err := router.repository.UpdateDeviceProfile(request.Context(), request.PathValue("deviceID"), body.ProfileCiphertext); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "device_not_found", "Device not found.")
		} else {
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update the device profile.")
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) revokeDevice(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.PathValue("deviceID")
	if !validUUID(deviceID) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device", "Device identifier is invalid.")
		return
	}
	if err := router.repository.RevokeDevice(request.Context(), deviceID, router.now()); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "device_not_found", "Device not found.")
		} else {
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to remove the device.")
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) createPairingToken(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	token, err := auth.NewOpaqueToken()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pairing_error", "Unable to create a pairing token.")
		return
	}
	tokenHash, err := auth.TokenHash(token)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "pairing_error", "Unable to create a pairing token.")
		return
	}
	expiresAt := router.now().Add(pairingTokenLifetime)
	if err := router.repository.CreatePairingToken(request.Context(), ownerID, tokenHash[:], expiresAt); err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to store the pairing token.")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"pairing_token": token,
		"expires_at":    expiresAt,
	})
}

func (router *router) pairDevice(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		PairingToken      string `json:"pairing_token"`
		DeviceUUID        string `json:"device_uuid"`
		ProfileCiphertext []byte `json:"profile_ciphertext"`
		IdentityPublicKey []byte `json:"identity_public_key"`
		CommandPublicKey  []byte `json:"command_public_key"`
		WrappedVaultKey   []byte `json:"wrapped_vault_key"`
		DeviceCredential  string `json:"device_credential"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	tokenHash, err := auth.TokenHash(body.PairingToken)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_pairing_token", domain.ErrInvalidPairingToken.Error())
		return
	}
	if !validUUID(body.DeviceUUID) || len(body.IdentityPublicKey) != ed25519.PublicKeySize || len(body.CommandPublicKey) != commandPublicKeyBytes || len(body.ProfileCiphertext) > maxProfileCiphertextBytes || len(body.WrappedVaultKey) > maxWrappedVaultKeyBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device", "Device identity or encrypted profile is invalid.")
		return
	}
	credentialHash, err := auth.TokenHash(body.DeviceCredential)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device", "Device credential is invalid.")
		return
	}
	record, err := router.repository.ConsumePairingToken(request.Context(), tokenHash[:], router.now(), store.DevicePairing{
		DeviceUUID:        body.DeviceUUID,
		ProfileCiphertext: body.ProfileCiphertext,
		IdentityPublicKey: body.IdentityPublicKey,
		CommandPublicKey:  body.CommandPublicKey,
		WrappedVaultKey:   body.WrappedVaultKey,
		CredentialHash:    credentialHash[:],
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidPairingToken):
			writeError(writer, http.StatusUnauthorized, "invalid_pairing_token", err.Error())
		case errors.Is(err, domain.ErrDeviceExists):
			writeError(writer, http.StatusConflict, "device_exists", err.Error())
		case errors.Is(err, domain.ErrNoNumericID):
			writeError(writer, http.StatusConflict, "numeric_ids_exhausted", err.Error())
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to pair the device.")
		}
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"device": record,
	})
}

func (router *router) insertEvent(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Envelope domain.EventEnvelope `json:"envelope"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	publicKey, err := router.repository.AuthenticateDevice(request.Context(), body.Envelope.DeviceUUID, token, router.now())
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	if err := e2ee.VerifyEnvelope(ed25519.PublicKey(publicKey), body.Envelope); err != nil {
		if errors.Is(err, e2ee.ErrInvalidEnvelope) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_event", err.Error())
			return
		}
		writeError(writer, http.StatusUnauthorized, "invalid_event_signature", "Event signature is invalid.")
		return
	}
	now := router.now()
	if body.Envelope.OccurredAt.After(now.Add(maxEventFutureSkew)) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_event_time", "Event timestamp is too far in the future.")
		return
	}
	expiresAt := body.Envelope.OccurredAt.Add(eventRetention)
	if !expiresAt.After(now) {
		writeError(writer, http.StatusUnprocessableEntity, "event_expired", "Event is outside the retention window.")
		return
	}
	body.Envelope.Sequence = 0
	sequence, created, err := router.repository.InsertEvent(request.Context(), body.Envelope, expiresAt)
	if err != nil {
		if errors.Is(err, domain.ErrIdempotencyConflict) {
			writeError(writer, http.StatusConflict, "idempotency_conflict", err.Error())
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to store the event.")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"sequence": sequence, "created": created})
}

func (router *router) listEvents(writer http.ResponseWriter, request *http.Request) {
	afterSequence, err := queryInt64(request, "after_sequence", 0)
	if err != nil || afterSequence < 0 {
		writeError(writer, http.StatusBadRequest, "invalid_cursor", "Event cursor must be a non-negative integer.")
		return
	}
	limit, err := queryInt(request, "limit", defaultEventPageSize)
	if err != nil || limit < 1 || limit > maxEventPageSize {
		writeError(writer, http.StatusBadRequest, "invalid_limit", "Event page size must be between 1 and 500.")
		return
	}
	events, err := router.repository.ListEvents(request.Context(), afterSequence, limit, router.now())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to read encrypted events.")
		return
	}
	nextSequence := afterSequence
	if len(events) > 0 {
		nextSequence = events[len(events)-1].Sequence
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"events":        events,
		"next_sequence": nextSequence,
	})
}

func queryInt64(request *http.Request, name string, fallback int64) (int64, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func queryInt(request *http.Request, name string, fallback int) (int, error) {
	value := request.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}

func (router *router) enqueueCommand(writer http.ResponseWriter, request *http.Request) {
	var encrypted domain.EncryptedCommand
	if err := decodeJSON(request, &encrypted); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	encrypted.DeviceUUID = request.PathValue("deviceID")
	now := router.now()
	if !validUUID(encrypted.ID) || !validUUID(encrypted.DeviceUUID) ||
		!validUUID(encrypted.SenderInstallationID) ||
		len(encrypted.SenderPublicKey) != commandPublicKeyBytes ||
		len(encrypted.Nonce) != commandNonceBytes ||
		len(encrypted.Ciphertext) == 0 || len(encrypted.Ciphertext) > maxCommandCiphertextBytes ||
		len(encrypted.Signature) != commandSignatureBytes ||
		encrypted.CreatedAt.After(now.Add(maxEventFutureSkew)) ||
		encrypted.CreatedAt.Before(now.Add(-maxEventFutureSkew)) ||
		!encrypted.ExpiresAt.After(now) ||
		encrypted.ExpiresAt.After(encrypted.CreatedAt.Add(maxCommandLifetime)) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_command", "Encrypted command metadata is invalid.")
		return
	}
	created, err := router.repository.EnqueueCommand(request.Context(), encrypted)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrIdempotencyConflict):
			writeError(writer, http.StatusConflict, "idempotency_conflict", err.Error())
		case errors.Is(err, pgx.ErrNoRows):
			writeError(writer, http.StatusNotFound, "device_not_found", "Device not found.")
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to queue the encrypted command.")
		}
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"id": encrypted.ID, "created": created})
}

func (router *router) claimCommand(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.URL.Query().Get("device_uuid")
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(deviceID) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), deviceID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	now := router.now()
	encrypted, err := router.repository.ClaimCommand(request.Context(), deviceID, now, now.Add(commandLease))
	if errors.Is(err, pgx.ErrNoRows) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to claim an encrypted command.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"command": encrypted})
}

func (router *router) completeCommand(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DeviceUUID string                        `json:"device_uuid"`
		Result     domain.EncryptedCommandResult `json:"result"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(body.DeviceUUID) || !validUUID(request.PathValue("commandID")) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if len(body.Result.Nonce) != commandNonceBytes || len(body.Result.Ciphertext) == 0 || len(body.Result.Ciphertext) > maxCommandCiphertextBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_command_result", "Encrypted command result is invalid.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), body.DeviceUUID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	err := router.repository.CompleteCommand(
		request.Context(),
		body.DeviceUUID,
		request.PathValue("commandID"),
		body.Result,
		router.now(),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(writer, http.StatusNotFound, "command_not_found", "Encrypted command was not found or has expired.")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to complete the encrypted command.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) commandStatus(writer http.ResponseWriter, request *http.Request) {
	status, err := router.repository.CommandStatus(
		request.Context(),
		request.PathValue("deviceID"),
		request.PathValue("commandID"),
		router.now(),
	)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(writer, http.StatusNotFound, "command_not_found", "Encrypted command was not found or has expired.")
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to read encrypted command status.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"command": status})
}

func (router *router) updateDeviceCommandKey(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DeviceUUID       string `json:"device_uuid"`
		CommandPublicKey []byte `json:"command_public_key"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(body.DeviceUUID) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if len(body.CommandPublicKey) != commandPublicKeyBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_command_key", "Device command public key is invalid.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), body.DeviceUUID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	if err := router.repository.UpdateDeviceCommandPublicKey(request.Context(), body.DeviceUUID, body.CommandPublicKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "device_not_found", "Device not found.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update the device command key.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) updateDeviceRadioStatus(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DeviceUUID            string `json:"device_uuid"`
		SlotKey               string `json:"slot_key"`
		SlotIndex             int    `json:"slot_index"`
		RadioAccessTechnology string `json:"radio_access_technology"`
		SignalBars            *int   `json:"signal_bars"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(body.DeviceUUID) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if body.SlotKey == "" {
		body.SlotKey = "1"
	}
	if body.SlotIndex == 0 {
		body.SlotIndex = 1
	}
	if len(body.SlotKey) > 64 || body.SlotIndex < 1 || body.SlotIndex > 32 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_slot_identity", "Device slot identity is invalid.")
		return
	}
	if !validRadioAccessTechnology(body.RadioAccessTechnology) ||
		(body.SignalBars != nil && (*body.SignalBars < 0 || *body.SignalBars > 4)) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_radio_status", "Device radio status is invalid.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), body.DeviceUUID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	if err := router.repository.UpdateDeviceRadioStatus(
		request.Context(),
		body.DeviceUUID,
		body.SlotKey,
		body.SlotIndex,
		body.RadioAccessTechnology,
		body.SignalBars,
		router.now(),
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "device_not_found", "Device not found.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update device radio status.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) updateDeviceSIMIdentity(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DeviceUUID     string `json:"device_uuid"`
		SlotKey        string `json:"slot_key"`
		SlotIndex      int    `json:"slot_index"`
		SIMFingerprint string `json:"sim_fingerprint"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(body.DeviceUUID) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if body.SlotKey == "" || len(body.SlotKey) > 64 || body.SlotIndex < 1 || body.SlotIndex > 32 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_slot_identity", "Device slot identity is invalid.")
		return
	}
	fingerprint, err := hex.DecodeString(body.SIMFingerprint)
	if err != nil || len(fingerprint) != 32 {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_sim_identity", "SIM fingerprint is invalid.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), body.DeviceUUID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	simID, numericID, err := router.repository.UpdateDeviceSIMIdentity(request.Context(), body.DeviceUUID, body.SlotKey, body.SlotIndex, fingerprint, router.now())
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeError(writer, http.StatusNotFound, "device_slot_not_found", "Device slot not found.")
			return
		case errors.Is(err, domain.ErrNoNumericID):
			writeError(writer, http.StatusConflict, "numeric_ids_exhausted", "No SIM numeric ID is available.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to synchronize the SIM identity.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sim_id": simID, "numeric_id": numericID})
}

func validRadioAccessTechnology(value string) bool {
	switch value {
	case "unknown", "gsm", "edge", "umts", "hspa", "lte", "nr_nsa", "nr_sa":
		return true
	default:
		return false
	}
}

func (router *router) bootstrapLogin(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	credential, err := router.repository.OwnerCredentialByUsername(request.Context(), body.Username)
	if err != nil || credential.PasswordDisabledAt != nil || credential.PasswordHash == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials.")
		return
	}
	valid, err := auth.VerifyPassword(body.Password, credential.PasswordHash)
	if err != nil || !valid {
		writeError(writer, http.StatusUnauthorized, "invalid_credentials", "Invalid credentials.")
		return
	}
	pair, err := auth.NewTokenPair(time.Now())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "authentication_error", "Unable to create a session.")
		return
	}
	if err := router.repository.CreateSession(request.Context(), credential.ID, pair); err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to store the session.")
		return
	}
	writeJSON(writer, http.StatusCreated, pair)
}

func (router *router) refreshSession(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	pair, err := auth.NewTokenPair(time.Now())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "authentication_error", "Unable to refresh the session.")
		return
	}
	if err := router.repository.RotateSession(request.Context(), body.RefreshToken, pair, time.Now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_refresh_token", "Refresh token is invalid or expired.")
		return
	}
	writeJSON(writer, http.StatusOK, pair)
}

func (router *router) logout(writer http.ResponseWriter, request *http.Request) {
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is required.")
		return
	}
	if err := router.repository.RevokeSession(request.Context(), token, time.Now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) requireOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		token, ok := bearerToken(request.Header.Get("Authorization"))
		if !ok {
			writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is required.")
			return
		}
		ownerID, err := router.repository.AuthenticateOwnerSession(request.Context(), token, router.now())
		if err != nil {
			writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
			return
		}
		context := context.WithValue(request.Context(), ownerContextKey{}, ownerID)
		next(writer, request.WithContext(context))
	}
}

func (router *router) now() time.Time {
	if router.config.Now != nil {
		return router.config.Now().UTC()
	}
	return time.Now().UTC()
}

func validUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	encoded := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	_, err := hex.Decode(decoded, []byte(encoded))
	return err == nil
}

func bearerToken(value string) (string, bool) {
	prefix, token, ok := strings.Cut(value, " ")
	return token, ok && strings.EqualFold(prefix, "Bearer") && strings.TrimSpace(token) != ""
}

func decodeJSON(request *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("invalid JSON body: multiple JSON values")
		}
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}

func withJSONContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(writer, request)
	})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string, message string) {
	writeJSON(writer, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// ParsePort converts an optional environment value into a validated port.
func ParsePort(value string, fallback int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}
