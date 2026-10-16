package api

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

const deviceDiscoveryProofDomain = "sim-deck-device-discovery-v1"

func (router *router) openDeviceDiscovery(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	now := router.now()
	session, err := router.repository.OpenDeviceDiscovery(request.Context(), ownerID, now, now.Add(deviceDiscoveryLifetime))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to open device discovery.")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"active": session.Active(now), "session": session})
}

func (router *router) getDeviceDiscoverySession(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	session, err := router.repository.GetDeviceDiscoverySession(request.Context(), ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(writer, http.StatusOK, map[string]any{"active": false})
		return
	}
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to read device discovery.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"active": session.Active(router.now()), "session": session})
}

func (router *router) closeDeviceDiscovery(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	if err := router.repository.CloseDeviceDiscovery(request.Context(), ownerID, router.now()); err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to close device discovery.")
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) listDeviceDiscoveries(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	records, err := router.repository.ListPendingDeviceDiscoveries(request.Context(), ownerID, router.now())
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to list discovered devices.")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"discoveries": records})
}

func (router *router) submitDeviceDiscovery(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		DeviceUUID        string `json:"device_uuid"`
		IdentityPublicKey []byte `json:"identity_public_key"`
		CommandPublicKey  []byte `json:"command_public_key"`
		PendingCredential string `json:"pending_credential"`
		Signature         []byte `json:"signature"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !validUUID(body.DeviceUUID) || len(body.IdentityPublicKey) != ed25519.PublicKeySize ||
		len(body.CommandPublicKey) != commandPublicKeyBytes || len(body.Signature) != ed25519.SignatureSize {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device_discovery", "Device discovery identity is invalid.")
		return
	}
	credentialHash, err := auth.TokenHash(body.PendingCredential)
	if err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device_discovery", "Pending device credential is invalid.")
		return
	}
	proof := deviceDiscoverySignedData(body.DeviceUUID, body.IdentityPublicKey, body.CommandPublicKey, body.PendingCredential)
	if !ed25519.Verify(ed25519.PublicKey(body.IdentityPublicKey), proof, body.Signature) {
		writeError(writer, http.StatusUnauthorized, "invalid_device_proof", "Device identity proof is invalid.")
		return
	}
	record, err := router.repository.SubmitDeviceDiscovery(request.Context(), router.now(), store.DeviceDiscoveryRequest{
		DeviceUUID: body.DeviceUUID, IdentityPublicKey: body.IdentityPublicKey,
		CommandPublicKey: body.CommandPublicKey, CredentialHash: credentialHash[:],
	})
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrDeviceDiscoveryClosed):
			writeError(writer, http.StatusConflict, "device_discovery_closed", err.Error())
		case errors.Is(err, domain.ErrDeviceDiscoveryConflict):
			writeError(writer, http.StatusConflict, "device_discovery_conflict", err.Error())
		case errors.Is(err, domain.ErrDeviceDiscoveryLimit):
			writeError(writer, http.StatusTooManyRequests, "device_discovery_limit", err.Error())
		case errors.Is(err, domain.ErrDeviceExists):
			writeError(writer, http.StatusConflict, "device_exists", err.Error())
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to submit device discovery.")
		}
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"discovery": record})
}

func (router *router) approveDeviceDiscovery(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	deviceID := request.PathValue("deviceID")
	if !validUUID(deviceID) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device_discovery", "Device identifier is invalid.")
		return
	}
	record, err := router.repository.ApproveDeviceDiscovery(request.Context(), ownerID, deviceID, router.now())
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeError(writer, http.StatusNotFound, "device_discovery_not_found", "Device discovery was not found.")
		case errors.Is(err, domain.ErrDeviceDiscoveryExpired):
			writeError(writer, http.StatusGone, "device_discovery_expired", err.Error())
		case errors.Is(err, domain.ErrDeviceDiscoveryRejected):
			writeError(writer, http.StatusConflict, "device_discovery_rejected", err.Error())
		case errors.Is(err, domain.ErrDeviceExists):
			writeError(writer, http.StatusConflict, "device_exists", err.Error())
		case errors.Is(err, domain.ErrNoNumericID):
			writeError(writer, http.StatusConflict, "numeric_ids_exhausted", err.Error())
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to approve device discovery.")
		}
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"device": record})
}

func (router *router) rejectDeviceDiscovery(writer http.ResponseWriter, request *http.Request) {
	ownerID, ok := request.Context().Value(ownerContextKey{}).(string)
	if !ok || ownerID == "" {
		writeError(writer, http.StatusUnauthorized, "invalid_access_token", "Access token is invalid or expired.")
		return
	}
	deviceID := request.PathValue("deviceID")
	if !validUUID(deviceID) {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_device_discovery", "Device identifier is invalid.")
		return
	}
	if err := router.repository.RejectDeviceDiscovery(request.Context(), ownerID, deviceID, router.now()); err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeError(writer, http.StatusNotFound, "device_discovery_not_found", "Device discovery was not found.")
		case errors.Is(err, domain.ErrDeviceDiscoveryExpired):
			writeError(writer, http.StatusGone, "device_discovery_expired", err.Error())
		case errors.Is(err, domain.ErrDeviceExists):
			writeError(writer, http.StatusConflict, "device_exists", err.Error())
		default:
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to reject device discovery.")
		}
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (router *router) deviceDiscoveryStatus(writer http.ResponseWriter, request *http.Request) {
	deviceID := request.PathValue("deviceID")
	credential, ok := bearerToken(request.Header.Get("Authorization"))
	if !ok || !validUUID(deviceID) {
		writeError(writer, http.StatusUnauthorized, "invalid_pending_credential", "Pending device credential is required.")
		return
	}
	credentialHash, err := auth.TokenHash(credential)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_pending_credential", "Pending device credential is invalid.")
		return
	}
	record, err := router.repository.DeviceDiscoveryStatus(request.Context(), deviceID, credentialHash[:], router.now())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(writer, http.StatusUnauthorized, "invalid_pending_credential", "Pending device credential is invalid or expired.")
		} else {
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to read device discovery status.")
		}
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"discovery": record})
}

func deviceDiscoverySignedData(deviceUUID string, identityPublicKey []byte, commandPublicKey []byte, credential string) []byte {
	data := make([]byte, 0, len(deviceDiscoveryProofDomain)+len(deviceUUID)+len(identityPublicKey)+len(commandPublicKey)+len(credential)+20)
	for _, field := range [][]byte{
		[]byte(deviceDiscoveryProofDomain), []byte(deviceUUID), identityPublicKey, commandPublicKey, []byte(credential),
	} {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field)))
		data = append(data, length[:]...)
		data = append(data, field...)
	}
	return data
}
