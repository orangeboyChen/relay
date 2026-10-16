package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"sim-deck-device/internal/agent"
	"sim-deck-device/internal/at"
	"sim-deck-device/internal/call"
	"sim-deck-device/internal/callevent"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/media"
	"sim-deck-device/internal/modem"
	"sim-deck-device/internal/radio"
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
	return runWithDependencies(ctx, config, logger, runtimeDependencies{
		listen: func(ctx context.Context, network, address string) (net.Listener, error) {
			var listener net.ListenConfig
			return listener.Listen(ctx, network, address)
		},
		runDevice:          runDevice,
		deviceDependencies: productionDependencies(nil),
	})
}

type runtimeDependencies struct {
	listen             func(context.Context, string, string) (net.Listener, error)
	runDevice          func(context.Context, agent.Config, *slog.Logger, *atomic.Bool, deviceDependencies) error
	deviceDependencies deviceDependencies
}

func runWithDependencies(ctx context.Context, config agent.Config, logger *slog.Logger, dependencies runtimeDependencies) error {
	var ready atomic.Bool
	listener, err := dependencies.listen(ctx, "tcp", config.HealthAddress)
	if err != nil {
		return fmt.Errorf("listen for health checks: %w", err)
	}
	server := healthServer(config.HealthAddress, &ready)
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	serverResult := make(chan error, 1)
	go func() {
		serverResult <- server.Serve(listener)
	}()
	deviceResult := make(chan error, 1)
	go func() {
		deviceResult <- dependencies.runDevice(runContext, config, logger, &ready, dependencies.deviceDependencies)
	}()

	var deviceError error
	var serverError error
	deviceCompleted := false
	serverCompleted := false
	select {
	case deviceError = <-deviceResult:
		deviceCompleted = true
		cancel()
	case serverError = <-serverResult:
		serverCompleted = true
		cancel()
	}

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	shutdownError := server.Shutdown(shutdownContext)
	if !deviceCompleted {
		deviceError = <-deviceResult
	}
	if !serverCompleted {
		serverError = <-serverResult
	}

	return errors.Join(
		meaningfulRuntimeError("device runtime", deviceError),
		meaningfulRuntimeError("health server", serverError),
		meaningfulRuntimeError("shut down health server", shutdownError),
	)
}

func meaningfulRuntimeError(operation string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

type deviceSession interface {
	Initialize(context.Context) error
	Run(context.Context) error
	Calls() *call.Controller
}

type commandRuntime interface {
	Run(context.Context) error
}

type callEventRuntime interface {
	Run(context.Context) error
	command.CallEventRegistrar
	incomingcall.EventRegistrar
}

type callMediaRuntime interface {
	command.CallMedia
	Errors() <-chan error
	Close() error
}

type deviceDependencies struct {
	openTransport    func(string, int) (io.ReadWriteCloser, error)
	bindIdentity     func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error)
	openInbox        func(string, time.Duration, []byte) (*sms.Inbox, error)
	newSession       func(*at.Client, *sms.Inbox, modem.MessageSink, *slog.Logger, time.Duration) (deviceSession, error)
	syncCommandKey   func(context.Context, agent.Config, identity.State) error
	newCallEvents    func(agent.Config, identity.State, <-chan call.Snapshot) (callEventRuntime, error)
	newIncomingCalls func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar) (commandRuntime, error)
	newCallMedia     func(agent.Config) (callMediaRuntime, error)
	newCommands      func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error)
	newRadioStatus   func(agent.Config, identity.State, *at.Client) (commandRuntime, error)
}

func productionDependencies(httpClient *http.Client) deviceDependencies {
	return deviceDependencies{
		openTransport: func(path string, baud int) (io.ReadWriteCloser, error) {
			return serialport.Open(path, baud)
		},
		bindIdentity: func(ctx context.Context, config agent.Config, store identity.Store, state identity.State) (identity.State, error) {
			return agent.EnsureBinding(ctx, config, store, state, serverapi.NewClient(config.ServerURL, httpClient))
		},
		openInbox: sms.NewPersistentInbox,
		newSession: func(client *at.Client, inbox *sms.Inbox, sink modem.MessageSink, logger *slog.Logger, timeout time.Duration) (deviceSession, error) {
			return modem.NewSession(client, inbox, sink, logger, timeout)
		},
		syncCommandKey: func(ctx context.Context, config agent.Config, state identity.State) error {
			return serverapi.NewClient(config.ServerURL, httpClient).UpdateCommandKey(ctx, state)
		},
		newCallEvents: func(config agent.Config, state identity.State, snapshots <-chan call.Snapshot) (callEventRuntime, error) {
			return callevent.NewPublisher(serverapi.NewClient(config.ServerURL, httpClient), state, snapshots)
		},
		newIncomingCalls: func(config agent.Config, state identity.State, snapshots <-chan call.Snapshot, events incomingcall.EventRegistrar) (commandRuntime, error) {
			return incomingcall.NewRuntime(serverapi.NewClient(config.ServerURL, httpClient), state, snapshots, events)
		},
		newCallMedia: func(config agent.Config) (callMediaRuntime, error) {
			return media.NewManager(func(ctx context.Context) (media.DuplexPCM, error) {
				switch config.AudioMode {
				case agent.AudioModeALSA:
					return media.OpenALSAEndpoint(ctx, config.ALSACaptureDevice, config.ALSAPlaybackDevice)
				case agent.AudioModeRawPCM:
					return media.OpenRawPCMEndpoint(config.PCMDevice)
				default:
					return nil, fmt.Errorf("unsupported audio mode %q", config.AudioMode)
				}
			})
		},
		newCommands: func(config agent.Config, state identity.State, calls command.Calls, dependencies command.Dependencies) (commandRuntime, error) {
			return command.NewRuntime(serverapi.NewClient(config.ServerURL, httpClient), state, calls, config.CommandPoll, dependencies)
		},
		newRadioStatus: func(config agent.Config, state identity.State, client *at.Client) (commandRuntime, error) {
			return radio.NewRuntime(
				client,
				serverapi.NewClient(config.ServerURL, httpClient),
				state,
				config.RadioStatusPoll,
				config.CommandTimeout,
			)
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

	inbox, err := dependencies.openInbox(
		filepath.Join(config.StateDirectory, "sms-inbox.json"),
		365*24*time.Hour,
		deviceIdentity.CommandPrivateKey,
	)
	if err != nil {
		return fmt.Errorf("open SMS inbox: %w", err)
	}
	messageSink := modem.MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
		logger.Warn("SMS retained pending encrypted synchronization", "message_id", message.ID, "segments", message.Segments)
		return modem.ErrMessageDeliveryDeferred
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
	if err := dependencies.syncCommandKey(ctx, config, deviceIdentity); err != nil {
		return fmt.Errorf("register device command key: %w", err)
	}
	events, err := dependencies.newCallEvents(config, deviceIdentity, session.Calls().Events())
	if err != nil {
		return fmt.Errorf("create encrypted call event runtime: %w", err)
	}
	incomingCalls, err := dependencies.newIncomingCalls(config, deviceIdentity, session.Calls().Subscribe(), events)
	if err != nil {
		return fmt.Errorf("create incoming call runtime: %w", err)
	}
	callMedia, err := dependencies.newCallMedia(config)
	if err != nil {
		return fmt.Errorf("create call media runtime: %w", err)
	}
	defer callMedia.Close()
	commands, err := dependencies.newCommands(config, deviceIdentity, session.Calls(), command.Dependencies{Events: events, Media: callMedia})
	if err != nil {
		return fmt.Errorf("create encrypted command runtime: %w", err)
	}
	radioStatus, err := dependencies.newRadioStatus(config, deviceIdentity, client)
	if err != nil {
		return fmt.Errorf("create radio status runtime: %w", err)
	}

	ready.Store(true)
	defer ready.Store(false)
	logger.Info(
		"device agent ready",
		"at_device", config.ATDevice,
		"audio_mode", config.AudioMode,
		"alsa_capture_device", config.ALSACaptureDevice,
		"alsa_playback_device", config.ALSAPlaybackDevice,
		"pcm_device", config.PCMDevice,
		"device_uuid", deviceIdentity.DeviceUUID,
		"numeric_id", deviceIdentity.NumericID,
	)
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	go logMediaErrors(runContext, callMedia.Errors(), logger)
	results := make(chan error, 5)
	go func() { results <- session.Run(runContext) }()
	go func() { results <- commands.Run(runContext) }()
	go func() { results <- events.Run(runContext) }()
	go func() { results <- incomingCalls.Run(runContext) }()
	go func() { results <- radioStatus.Run(runContext) }()
	first := <-results
	cancel()
	resultsToCheck := []error{first}
	for count := 1; count < 5; count++ {
		resultsToCheck = append(resultsToCheck, <-results)
	}
	for _, result := range resultsToCheck {
		if result != nil && !errors.Is(result, context.Canceled) {
			return result
		}
	}
	return resultsToCheck[len(resultsToCheck)-1]
}

func logMediaErrors(ctx context.Context, failures <-chan error, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-failures:
			if !ok {
				return
			}
			logger.Error("call media stopped", "error", err)
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
