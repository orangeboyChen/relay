package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"sim-deck-device/internal/agent"
	"sim-deck-device/internal/at"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/serialport"
	"sim-deck-device/internal/serverapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	config, err := agent.LoadConfig(os.LookupEnv)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, config, logger); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("device agent stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, config agent.Config, logger *slog.Logger) error {
	var ready atomic.Bool
	server := healthServer(config.HealthAddress, &ready)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("health server stopped", "error", err)
		}
	}()
	defer func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()

	transport, err := serialport.Open(config.ATDevice, config.Baud)
	if err != nil {
		return err
	}
	client := at.NewClient(transport)
	defer client.Close()
	identityStore := identity.NewStore(config.StateDirectory)
	deviceIdentity, err := identityStore.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load device identity: %w", err)
	}

	for _, command := range []string{"ATE0", "AT+CMEE=2", "AT+CMGF=0"} {
		commandContext, cancel := context.WithTimeout(ctx, config.CommandTimeout)
		response, commandError := client.Send(commandContext, command)
		cancel()
		if commandError != nil {
			return commandError
		}
		if !response.OK() {
			return errors.New("modem rejected initialization command " + command)
		}
	}
	deviceIdentity, err = agent.EnsureBinding(ctx, config, identityStore, deviceIdentity, serverapi.NewClient(config.ServerURL, nil))
	if err != nil {
		return fmt.Errorf("bind device to server: %w", err)
	}

	ready.Store(true)
	defer ready.Store(false)
	logger.Info(
		"device agent ready",
		"at_device", config.ATDevice,
		"pcm_device", config.PCMDevice,
		"sound_device", config.SoundDevice,
		"device_uuid", deviceIdentity.DeviceUUID,
		"numeric_id", deviceIdentity.NumericID,
	)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case readError := <-client.Errors():
			return readError
		case urc := <-client.URCs():
			logger.Info("modem notification", "urc", urc)
		}
	}
}

func healthServer(address string, ready *atomic.Bool) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		status := http.StatusServiceUnavailable
		if ready.Load() {
			status = http.StatusOK
		}
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(map[string]bool{"ready": ready.Load()})
	})
	return &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
