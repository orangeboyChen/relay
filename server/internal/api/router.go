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

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

// Repository defines the encrypted persistence operations exposed by the API.
type Repository interface {
	ListDevices(context.Context) ([]store.DeviceRecord, error)
	UpdateNumericID(context.Context, string, int) error
	InsertEvent(context.Context, domain.EventEnvelope, time.Time) (int64, bool, error)
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
