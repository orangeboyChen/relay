package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"sim-deck-device/internal/agent"
	"sim-deck-device/internal/at"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/modem"
	"sim-deck-device/internal/serialport"
	"sim-deck-device/internal/serverapi"
	"sim-deck-device/internal/sms"
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
	return runDevice(ctx, config, logger, &ready, productionDependencies())
}

type deviceSession interface {
	Initialize(context.Context) error
	Run(context.Context) error
}

type deviceDependencies struct {
	openTransport func(string, int) (io.ReadWriteCloser, error)
	bindIdentity  func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error)
	openInbox     func(string, time.Duration) (*sms.Inbox, error)
	newSession    func(*at.Client, *sms.Inbox, modem.MessageSink, *slog.Logger, time.Duration) (deviceSession, error)
}

func productionDependencies() deviceDependencies {
	return deviceDependencies{
		openTransport: func(path string, baud int) (io.ReadWriteCloser, error) {
			return serialport.Open(path, baud)
		},
		bindIdentity: func(ctx context.Context, config agent.Config, store identity.Store, state identity.State) (identity.State, error) {
			return agent.EnsureBinding(ctx, config, store, state, serverapi.NewClient(config.ServerURL, nil))
		},
		openInbox: sms.NewPersistentInbox,
		newSession: func(client *at.Client, inbox *sms.Inbox, sink modem.MessageSink, logger *slog.Logger, timeout time.Duration) (deviceSession, error) {
			return modem.NewSession(client, inbox, sink, logger, timeout)
		},
	}
}

func runDevice(ctx context.Context, config agent.Config, logger *slog.Logger, ready *atomic.Bool, dependencies deviceDependencies) error {
	transport, err := dependencies.openTransport(config.ATDevice, config.Baud)
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

	inbox, err := dependencies.openInbox(filepath.Join(config.StateDirectory, "sms-inbox.json"), 365*24*time.Hour)
	if err != nil {
		return fmt.Errorf("open SMS inbox: %w", err)
	}
	messageSink := modem.MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
		logger.Info("SMS ready for encrypted synchronization", "message_id", message.ID, "segments", message.Segments)
		return nil
	})
	session, err := dependencies.newSession(client, inbox, messageSink, logger, config.CommandTimeout)
	if err != nil {
		return err
	}
	if err := session.Initialize(ctx); err != nil {
		return err
	}
	deviceIdentity, err = dependencies.bindIdentity(ctx, config, identityStore, deviceIdentity)
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
	return session.Run(ctx)
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
