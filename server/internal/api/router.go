package api

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	serverwebauthn "sim-deck-server/internal/webauthn"
)

// Repository defines the encrypted persistence operations exposed by the API.
type Repository interface {
	ListDevices(context.Context) ([]store.DeviceRecord, error)
	ListSIMCards(context.Context) ([]store.SIMRecord, error)
	RetireSIMCard(context.Context, string, time.Time) error
	UpdateNumericID(context.Context, string, int) error
	UpdateDeviceProfile(context.Context, string, []byte) error
	UpdateSIMNumericID(context.Context, string, int) error
	UpdateSIMProfile(context.Context, string, []byte) error
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
	UpdateDeviceSIMIdentity(context.Context, string, string, int, []byte, time.Time) (store.SIMSlotAssignment, error)
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
	TURNURL       string
	TURNSecret    []byte
	TURNTTL       time.Duration
	Now           func() time.Time
	Logger        *slog.Logger
}

// Handler builds the versioned HTTP API.
func Handler(repository Repository, config Config) http.Handler {
	router := &router{repository: repository, config: config}
	if passkeyStore, ok := repository.(serverwebauthn.Store); ok && config.PublicBaseURL != "" {
		router.webAuthn, _ = serverwebauthn.New(passkeyStore, config.PublicBaseURL)
	}
	mux := http.NewServeMux()
	mountSyncService(mux, router)
	mountAuthenticationService(mux, router)
	mountOwnerService(mux, router)
	mountDeviceService(mux, router)
	mux.HandleFunc("GET /healthz", router.health)
	mux.HandleFunc("GET /.well-known/sim-deck", router.discovery)
	mux.HandleFunc("GET /.well-known/apple-app-site-association", router.appleAppSiteAssociation)
	return withRequestLogging(withJSONContentType(mux), config.Logger)
}

type router struct {
	repository Repository
	config     Config
	webAuthn   *serverwebauthn.Service
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
	writeJSON(writer, http.StatusOK, map[string]any{
		"api_version": "v1",
		"media":       map[string]any{"transport": "webrtc", "relay": "turn"},
	})
}

func (router *router) appleAppSiteAssociation(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writeJSON(writer, http.StatusOK, map[string]any{
		"webcredentials": map[string]any{
			"apps": []string{"97YACL9S6W.com.nowcent.simdeck"},
		},
	})
}

func (router *router) turnCredentialsForOwner(writer http.ResponseWriter, request *http.Request) {
	router.writeTURNCredentials(writer, request.Context().Value(ownerContextKey{}) != nil)
}

func (router *router) turnCredentialsForDevice(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.PathValue("deviceID")
	token, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(deviceID) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is required.")
		return
	}
	if _, err := router.repository.AuthenticateDevice(request.Context(), deviceID, token, router.now()); err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_device_credential", "Device credential is invalid or revoked.")
		return
	}
	router.writeTURNCredentials(writer, true)
}

func (router *router) writeTURNCredentials(writer http.ResponseWriter, authenticated bool) {
	if !authenticated || strings.TrimSpace(router.config.TURNURL) == "" || len(router.config.TURNSecret) == 0 {
		writeError(writer, http.StatusServiceUnavailable, "turn_unavailable", "TURN media relay is not configured.")
		return
	}
	ttl := router.config.TURNTTL
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	credentials := NewTURNCredentials(router.config.TURNURL, router.config.TURNSecret, router.now(), ttl)
	writeJSON(writer, http.StatusOK, credentials)
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

func (router *router) listSIMCards(writer http.ResponseWriter, request *http.Request) {
	cards, err := router.repository.ListSIMCards(request.Context())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to list SIM cards.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"sims": cards})
}

func (router *router) retireSIMCard(writer http.ResponseWriter, request *http.Request) {
	simID := request.PathValue("simID")
	if !validUUID(simID) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_sim", "SIM identifier is invalid.")
		return
	}
	if err := router.repository.RetireSIMCard(request.Context(), simID, router.now()); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "sim_not_found", "SIM card not found or already removed.")
			return
		}
		if errors.Is(err, domain.ErrSIMAssigned) {
			writeError(writer, http.StatusConflict, "sim_online", "Remove the SIM card from its device before deleting it.")
			return
		}
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to remove the SIM card.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
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

func (router *router) updateSIMNumericID(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		NumericID int `json:"numeric_id"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(request.PathValue("simID")) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_sim", "SIM identifier is invalid.")
		return
	}
	if err := router.repository.UpdateSIMNumericID(request.Context(), request.PathValue("simID"), body.NumericID); err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidNumericID):
			writeError(writer, http.StatusUnprocessableEntity, "invalid_numeric_id", err.Error())
		case errors.Is(err, domain.ErrDuplicateNumericID):
			writeError(writer, http.StatusConflict, "duplicate_numeric_id", err.Error())
		case errors.Is(err, pgx.ErrNoRows):
			writeError(writer, http.StatusNotFound, "sim_not_found", "SIM not found.")
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update the SIM.")
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) updateSIMProfile(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		ProfileCiphertext []byte `json:"profile_ciphertext"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(request.PathValue("simID")) || len(body.ProfileCiphertext) == 0 || len(body.ProfileCiphertext) > maxProfileCiphertextBytes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_sim_profile", "Encrypted SIM profile is invalid.")
		return
	}
	if err := router.repository.UpdateSIMProfile(request.Context(), request.PathValue("simID"), body.ProfileCiphertext); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusNotFound, "sim_not_found", "SIM not found.")
		} else {
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update the SIM profile.")
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
	assignment, err := router.repository.UpdateDeviceSIMIdentity(request.Context(), body.DeviceUUID, body.SlotKey, body.SlotIndex, fingerprint, router.now())
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
	writeJSON(writer, http.StatusOK, assignment)
}

func validRadioAccessTechnology(value string) bool {
	switch value {
	case "unknown", "gsm", "edge", "umts", "hspa", "lte", "nr_nsa", "nr_sa":
		return true
	default:
		return false
	}
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
	parts := strings.Fields(value)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
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
	if recorder, ok := writer.(interface{ recordAPIError(string, string) }); ok {
		recorder.recordAPIError(code, message)
	}
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
