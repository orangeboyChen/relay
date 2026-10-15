package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

// Repository defines the encrypted persistence operations exposed by the API.
type Repository interface {
	ListDevices(context.Context) ([]store.DeviceRecord, error)
	UpdateNumericID(context.Context, string, int) error
	InsertEvent(context.Context, domain.EventEnvelope, time.Time) (int64, bool, error)
	OwnerCredentialByUsername(context.Context, string) (store.OwnerCredential, error)
	CreateSession(context.Context, string, auth.TokenPair) error
	RotateSession(context.Context, string, auth.TokenPair, time.Time) error
	RevokeSession(context.Context, string, time.Time) error
}

// Config describes public endpoints advertised to clients.
type Config struct {
	PublicBaseURL string
	MediaUDPPort  int
	MediaTCPPort  int
}

// Handler builds the versioned HTTP API.
func Handler(repository Repository, config Config) http.Handler {
	router := &router{repository: repository, config: config}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", router.health)
	mux.HandleFunc("GET /.well-known/sim-deck", router.discovery)
	mux.HandleFunc("GET /v1/devices", router.listDevices)
	mux.HandleFunc("PATCH /v1/devices/{deviceID}/numeric-id", router.updateNumericID)
	mux.HandleFunc("POST /v1/events", router.insertEvent)
	mux.HandleFunc("POST /v1/auth/bootstrap", router.bootstrapLogin)
	mux.HandleFunc("POST /v1/auth/refresh", router.refreshSession)
	mux.HandleFunc("POST /v1/auth/logout", router.logout)
	return withJSONContentType(mux)
}

type router struct {
	repository Repository
	config     Config
}

func (router *router) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (router *router) discovery(writer http.ResponseWriter, _ *http.Request) {
	baseURL := strings.TrimRight(router.config.PublicBaseURL, "/")
	writeJSON(writer, http.StatusOK, map[string]any{
		"api_version":   "v1",
		"websocket_url": baseURL + "/v1/events/ws",
		"media": map[string]int{
			"udp_port": router.config.MediaUDPPort,
			"tcp_port": router.config.MediaTCPPort,
		},
	})
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

func (router *router) insertEvent(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Envelope  domain.EventEnvelope `json:"envelope"`
		ExpiresAt time.Time            `json:"expires_at"`
	}
	if err := decodeJSON(request, &body); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if body.Envelope.ID == "" || body.Envelope.DeviceUUID == "" || body.Envelope.Type == "" || body.ExpiresAt.IsZero() {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_event", "Event routing metadata is incomplete.")
		return
	}
	sequence, created, err := router.repository.InsertEvent(request.Context(), body.Envelope, body.ExpiresAt)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to store the event.")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(writer, status, map[string]any{"sequence": sequence, "created": created})
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
