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
	"sim-deck-device/internal/messageevent"
	"sim-deck-device/internal/modem"
	"sim-deck-device/internal/radio"
	"sim-deck-device/internal/serialport"
	"sim-deck-device/internal/serverapi"
	"sim-deck-device/internal/slotpolicy"
	"sim-deck-device/internal/sms"
)

var errSIMIdentityChanged = errors.New("SIM identity changed; restart the device agent to reload event identity")

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
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
	logger.Info(
		"device agent starting",
		"server_url", config.ServerURL,
		"at_device", config.ATDevice,
		"audio_mode", config.AudioMode,
		"health_address", config.HealthAddress,
	)
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
	ClearOrphanedCalls(context.Context) error
	ReadSIMICCID(context.Context) (string, error)
	ReadSIMSlotStatus(context.Context) ([]modem.SIMSlotStatus, error)
	Run(context.Context) error
	Calls() *call.Controller
	command.Messages
	RetryPendingSMS(context.Context) error
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

type messageEventPublisher interface {
	Publish(context.Context, sms.ReceivedMessage) error
}

type slotPolicyRuntime interface {
	command.SlotSettings
	Restore(context.Context) error
}

type simProfileReader interface {
	ReadSIMProfile(context.Context) (modem.SIMProfile, error)
}

type simProfilePublisher interface {
	PublishSIMProfile(context.Context, messageevent.SIMProfile) error
}

type deviceDependencies struct {
	openTransport               func(string, int, string, string, string) (io.ReadWriteCloser, error)
	resolveHardware             func(context.Context, agent.Config, *at.Client) (string, string, error)
	bindIdentity                func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error)
	openInbox                   func(string, time.Duration, []byte) (*sms.Inbox, error)
	newSession                  func(*at.Client, *sms.Inbox, modem.MessageSink, *slog.Logger, time.Duration) (deviceSession, error)
	configureAudio              func(context.Context, agent.Config, *at.Client) error
	syncCommandKey              func(context.Context, agent.Config, identity.State) error
	syncSIMIdentity             func(context.Context, agent.Config, identity.State) (serverapi.SIMAssignment, error)
	newCallEvents               func(agent.Config, identity.State, <-chan call.Snapshot) (callEventRuntime, error)
	newIncomingCalls            func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar, incomingcall.CallRejecter, incomingcall.Policy) (commandRuntime, error)
	newSlotPolicy               func(agent.Config, *at.Client) (slotPolicyRuntime, error)
	newCallMedia                func(agent.Config) (callMediaRuntime, error)
	newCallMediaWithICE         func(agent.Config, []media.ICEServer) (callMediaRuntime, error)
	newCallMediaWithICEProvider func(agent.Config, identity.State, func(context.Context, agent.Config, identity.State) ([]media.ICEServer, error)) (callMediaRuntime, error)
	fetchTURNCredentials        func(context.Context, agent.Config, identity.State) ([]media.ICEServer, error)
	newMessageEvents            func(agent.Config, identity.State) (messageEventPublisher, error)
	newCommands                 func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error)
	newRadioStatus              func(agent.Config, identity.State, *at.Client) (commandRuntime, error)
}

func productionDependencies(httpClient *http.Client) deviceDependencies {
	return deviceDependencies{
		openTransport: func(path string, baud int, tlsCAFile string, tlsCertificateFile string, tlsKeyFile string) (io.ReadWriteCloser, error) {
			return serialport.Open(path, baud, tlsCAFile, tlsCertificateFile, tlsKeyFile)
		},
		resolveHardware: func(ctx context.Context, config agent.Config, client *at.Client) (string, string, error) {
			if config.DeviceUUID != "" {
				return config.DeviceUUID, config.SlotKey, nil
			}
			equipmentIdentity, err := modem.ReadEquipmentIdentity(ctx, client, config.CommandTimeout)
			if err != nil {
				return "", "", err
			}
			slotIndexes, slotError := modem.ReadSIMSlotIndexes(ctx, client, config.CommandTimeout)
			if slotError != nil {
				slotIndexes = []int{config.SlotIndex}
			}
			slotKeys := make([]string, 0, len(slotIndexes))
			for _, slotIndex := range slotIndexes {
				key, keyError := identity.HardwareSlotKey(equipmentIdentity, slotIndex)
				if keyError != nil {
					return "", "", keyError
				}
				slotKeys = append(slotKeys, key)
			}
			slotKey, err := identity.HardwareSlotKey(equipmentIdentity, config.SlotIndex)
			if err != nil {
				return "", "", err
			}
			deviceUUID, err := identity.DeriveDeviceUUID(slotKeys)
			return deviceUUID, slotKey, err
		},
		bindIdentity: func(ctx context.Context, config agent.Config, store identity.Store, state identity.State) (identity.State, error) {
			return agent.EnsureBinding(ctx, config, store, state, serverapi.NewClient(config.ServerURL, httpClient))
		},
		openInbox: sms.NewPersistentInbox,
		newSession: func(client *at.Client, inbox *sms.Inbox, sink modem.MessageSink, logger *slog.Logger, timeout time.Duration) (deviceSession, error) {
			return modem.NewSession(client, inbox, sink, logger, timeout)
		},
		configureAudio: func(ctx context.Context, config agent.Config, client *at.Client) error {
			switch config.AudioMode {
			case agent.AudioModeALSA:
				if err := modem.ConfigureUAC(ctx, client, config.CommandTimeout); err != nil {
					return err
				}
				return media.ValidateALSADevices(ctx, config.ALSACaptureDevice, config.ALSAPlaybackDevice)
			case agent.AudioModeRemotePCM:
				return modem.ConfigureUAC(ctx, client, config.CommandTimeout)
			}
			return nil
		},
		syncCommandKey: func(ctx context.Context, config agent.Config, state identity.State) error {
			return serverapi.NewClient(config.ServerURL, httpClient).UpdateCommandKey(ctx, state)
		},
		syncSIMIdentity: func(ctx context.Context, config agent.Config, state identity.State) (serverapi.SIMAssignment, error) {
			return serverapi.NewClient(config.ServerURL, httpClient).UpdateSIMIdentity(ctx, state)
		},
		newCallEvents: func(config agent.Config, state identity.State, snapshots <-chan call.Snapshot) (callEventRuntime, error) {
			return callevent.NewPublisher(serverapi.NewClient(config.ServerURL, httpClient), state, snapshots)
		},
		newIncomingCalls: func(config agent.Config, state identity.State, snapshots <-chan call.Snapshot, events incomingcall.EventRegistrar, calls incomingcall.CallRejecter, policy incomingcall.Policy) (commandRuntime, error) {
			return incomingcall.NewRuntime(serverapi.NewClient(config.ServerURL, httpClient), state, snapshots, events, calls, policy)
		},
		newSlotPolicy: func(config agent.Config, client *at.Client) (slotPolicyRuntime, error) {
			return slotpolicy.NewController(config.StateDirectory, client, config.CommandTimeout)
		},
		newCallMedia: func(config agent.Config) (callMediaRuntime, error) {
			return newCallMedia(config, nil)
		},
		newCallMediaWithICE: func(config agent.Config, servers []media.ICEServer) (callMediaRuntime, error) {
			return newCallMedia(config, servers)
		},
		newCallMediaWithICEProvider: func(config agent.Config, state identity.State, provider func(context.Context, agent.Config, identity.State) ([]media.ICEServer, error)) (callMediaRuntime, error) {
			return newCallMediaWithProvider(config, state, provider)
		},
		fetchTURNCredentials: func(ctx context.Context, config agent.Config, state identity.State) ([]media.ICEServer, error) {
			return serverapi.NewClient(config.ServerURL, httpClient).FetchTURNCredentials(ctx, state)
		},
		newMessageEvents: func(config agent.Config, state identity.State) (messageEventPublisher, error) {
			return messageevent.NewPublisher(serverapi.NewClient(config.ServerURL, httpClient), state)
		},
		newCommands: func(config agent.Config, state identity.State, calls command.Calls, dependencies command.Dependencies) (commandRuntime, error) {
			return command.NewRuntime(serverapi.NewClient(config.ServerURL, httpClient), state, calls, config.CommandPoll, dependencies)
		},
		newRadioStatus: func(config agent.Config, state identity.State, client *at.Client) (commandRuntime, error) {
			return radio.NewRuntimeWithLogger(
				client,
				serverapi.NewClient(config.ServerURL, httpClient),
				state,
				config.RadioStatusPoll,
				config.CommandTimeout,
				slog.Default(),
			)
		},
	}
}

func newCallMedia(config agent.Config, servers []media.ICEServer) (callMediaRuntime, error) {
	return newCallMediaWithProvider(config, identity.State{}, func(context.Context, agent.Config, identity.State) ([]media.ICEServer, error) {
		return servers, nil
	})
}

func newCallMediaWithProvider(config agent.Config, state identity.State, provider func(context.Context, agent.Config, identity.State) ([]media.ICEServer, error)) (callMediaRuntime, error) {
	return media.NewManagerWithICEProvider(media.ICEConfig{}, func(ctx context.Context) ([]media.ICEServer, error) {
		return provider(ctx, config, state)
	}, func(ctx context.Context) (media.DuplexPCM, error) {
		switch config.AudioMode {
		case agent.AudioModeALSA:
			return media.OpenALSAEndpoint(ctx, config.ALSACaptureDevice, config.ALSAPlaybackDevice)
		case agent.AudioModeRawPCM:
			return media.OpenRawPCMEndpoint(config.PCMDevice)
		case agent.AudioModeRemotePCM:
			return media.OpenRemotePCMEndpoint(ctx, config.PCMEndpoint, config.PCMTLSCAFile, config.PCMTLSCertificateFile, config.PCMTLSKeyFile)
		default:
			return nil, fmt.Errorf("unsupported audio mode %q", config.AudioMode)
		}
	})
}

func runDevice(ctx context.Context, config agent.Config, logger *slog.Logger, ready *atomic.Bool, dependencies deviceDependencies) error {
	if config.SlotKey == "" {
		config.SlotKey = "1"
	}
	if config.SlotIndex < 1 {
		config.SlotIndex = 1
	}
	transport, err := dependencies.openTransport(config.ATDevice, config.Baud, config.ATTLSCAFile, config.ATTLSCertificateFile, config.ATTLSKeyFile)
	if err != nil {
		return err
	}
	client := at.NewClient(transport)
	defer client.Close()
	deviceUUID, slotKey, err := dependencies.resolveHardware(ctx, config, client)
	if err != nil {
		return fmt.Errorf("derive device hardware identity: %w", err)
	}
	config.DeviceUUID = deviceUUID
	config.SlotKey = slotKey
	identityStore := identity.NewStore(config.StateDirectory, config.DeviceUUID)
	deviceIdentity, err := identityStore.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load device identity: %w", err)
	}
	deviceIdentity, err = identityStore.ConfigureSlot(deviceIdentity, config.SlotKey, config.SlotIndex)
	if err != nil {
		return fmt.Errorf("configure modem slot identity: %w", err)
	}
	logger.Info(
		"device identity ready for owner verification",
		"device_uuid", deviceIdentity.DeviceUUID,
		"identity_fingerprint", deviceIdentity.IdentityFingerprint(),
	)
	policy, err := dependencies.newSlotPolicy(config, client)
	if err != nil {
		return fmt.Errorf("open slot policy: %w", err)
	}

	inbox, err := dependencies.openInbox(
		filepath.Join(config.StateDirectory, "sms-inbox.json"),
		365*24*time.Hour,
		deviceIdentity.CommandPrivateKey,
	)
	if err != nil {
		return fmt.Errorf("open SMS inbox: %w", err)
	}
	var messageEvents messageEventPublisher
	messageSink := modem.MessageSinkFunc(func(ctx context.Context, message sms.ReceivedMessage) error {
		if !policy.Current().IncomingSMSEnabled {
			logger.Info("SMS retained because incoming SMS is disabled", "message_id", message.ID, "segments", message.Segments)
			return modem.ErrMessageDeliveryDeferred
		}
		if messageEvents == nil {
			return modem.ErrMessageDeliveryDeferred
		}
		if err := messageEvents.Publish(ctx, message); err != nil {
			logger.Warn("SMS retained pending encrypted synchronization", "message_id", message.ID, "segments", message.Segments, "error", err)
			return modem.ErrMessageDeliveryDeferred
		}
		return nil
	})
	session, err := dependencies.newSession(client, inbox, messageSink, logger, config.CommandTimeout)
	if err != nil {
		return err
	}
	if err := session.Initialize(ctx); err != nil {
		return err
	}
	if err := session.ClearOrphanedCalls(ctx); err != nil {
		return fmt.Errorf("reconcile modem calls after startup: %w", err)
	}
	if err := policy.Restore(ctx); err != nil {
		return fmt.Errorf("restore slot policy: %w", err)
	}
	if slots, slotError := session.ReadSIMSlotStatus(ctx); slotError == nil {
		logger.Info("SIM slot status detected", "slots", slots)
	} else if !errors.Is(slotError, modem.ErrSIMSlotUnsupported) {
		logger.Warn("SIM slot status unavailable", "error", slotError)
	}
	if iccid, iccidError := session.ReadSIMICCID(ctx); iccidError == nil {
		fingerprint, fingerprintError := deviceIdentity.FingerprintSIM(iccid)
		if fingerprintError != nil {
			return fmt.Errorf("fingerprint SIM identity: %w", fingerprintError)
		}
		deviceIdentity, err = identityStore.SetSIMFingerprint(deviceIdentity, fingerprint)
		if err != nil {
			return fmt.Errorf("persist SIM identity fingerprint: %w", err)
		}
		logger.Info("SIM card identity detected", "slot_key", deviceIdentity.SlotKey, "slot_index", deviceIdentity.SlotIndex)
	} else {
		logger.Warn("SIM card identity is unavailable; using slot-local identity", "slot_key", deviceIdentity.SlotKey, "error", iccidError)
	}
	if err := dependencies.configureAudio(ctx, config, client); err != nil {
		return fmt.Errorf("configure modem audio: %w", err)
	}
	deviceIdentity, err = dependencies.bindIdentity(ctx, config, identityStore, deviceIdentity)
	if err != nil {
		return fmt.Errorf("bind device to server: %w", err)
	}
	if err := dependencies.syncCommandKey(ctx, config, deviceIdentity); err != nil {
		return fmt.Errorf("register device command key: %w", err)
	}
	if dependencies.syncSIMIdentity == nil {
		return errors.New("SIM identity synchronization is not configured")
	}
	if deviceIdentity.SIMFingerprint != "" {
		assignment, syncError := dependencies.syncSIMIdentity(ctx, config, deviceIdentity)
		if syncError != nil {
			return fmt.Errorf("synchronize current SIM identity: %w", syncError)
		}
		deviceIdentity, err = identityStore.SetServerSIMAssignment(
			deviceIdentity, assignment.SlotID, deviceIdentity.SIMFingerprint, assignment.SIMID, assignment.NumericID,
		)
		if err != nil {
			return fmt.Errorf("persist SIM assignment: %w", err)
		}
	}
	messageEvents, err = dependencies.newMessageEvents(config, deviceIdentity)
	if err != nil {
		return fmt.Errorf("create encrypted message event publisher: %w", err)
	}
	if reader, readable := session.(simProfileReader); readable {
		if publisher, publishable := messageEvents.(simProfilePublisher); publishable {
			profile, readError := reader.ReadSIMProfile(ctx)
			if readError != nil {
				logger.Warn("SIM profile is unavailable", "slot_key", deviceIdentity.SlotKey, "slot_index", deviceIdentity.SlotIndex, "error", readError)
			} else if profile.PhoneNumber == "" && profile.Operator == "" {
				logger.Warn("SIM profile is empty", "slot_key", deviceIdentity.SlotKey, "slot_index", deviceIdentity.SlotIndex, "subscriber_number_provisioned", false, "operator_available", false)
			} else if publishError := publisher.PublishSIMProfile(ctx, messageevent.SIMProfile{PhoneNumber: profile.PhoneNumber, LineName: profile.Operator}); publishError != nil {
				logger.Warn("encrypted SIM profile synchronization failed", "slot_key", deviceIdentity.SlotKey, "slot_index", deviceIdentity.SlotIndex, "subscriber_number_provisioned", profile.PhoneNumber != "", "operator_available", profile.Operator != "", "error", publishError)
			} else {
				logger.Info("encrypted SIM profile synchronized", "slot_key", deviceIdentity.SlotKey, "slot_index", deviceIdentity.SlotIndex, "subscriber_number_provisioned", profile.PhoneNumber != "", "operator_available", profile.Operator != "")
			}
		}
	}
	if err := session.RetryPendingSMS(ctx); err != nil {
		return fmt.Errorf("synchronize pending SMS: %w", err)
	}
	events, err := dependencies.newCallEvents(config, deviceIdentity, session.Calls().Events())
	if err != nil {
		return fmt.Errorf("create encrypted call event runtime: %w", err)
	}
	incomingCalls, err := dependencies.newIncomingCalls(config, deviceIdentity, session.Calls().Subscribe(), events, session.Calls(), policy)
	if err != nil {
		return fmt.Errorf("create incoming call runtime: %w", err)
	}
	var callMedia callMediaRuntime
	if dependencies.newCallMediaWithICEProvider != nil && dependencies.fetchTURNCredentials != nil {
		callMedia, err = dependencies.newCallMediaWithICEProvider(config, deviceIdentity, dependencies.fetchTURNCredentials)
	} else if dependencies.newCallMediaWithICE != nil {
		callMedia, err = dependencies.newCallMediaWithICE(config, nil)
	} else {
		callMedia, err = dependencies.newCallMedia(config)
	}
	if err != nil {
		return fmt.Errorf("create call media runtime: %w", err)
	}
	defer callMedia.Close()
	commands, err := dependencies.newCommands(config, deviceIdentity, session.Calls(), command.Dependencies{Events: events, Media: callMedia, Messages: session, SlotSettings: policy, Logger: logger})
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
	go handleMediaErrors(runContext, callMedia.Errors(), session.Calls(), logger)
	results := make(chan error, 6)
	go func() { results <- session.Run(runContext) }()
	go func() { results <- commands.Run(runContext) }()
	go func() { results <- events.Run(runContext) }()
	go func() { results <- incomingCalls.Run(runContext) }()
	go func() { results <- radioStatus.Run(runContext) }()
	go func() {
		results <- synchronizeSIMIdentity(runContext, session, config, identityStore, deviceIdentity, dependencies.syncSIMIdentity, logger)
	}()
	first := <-results
	cancel()
	resultsToCheck := []error{first}
	for count := 1; count < 6; count++ {
		resultsToCheck = append(resultsToCheck, <-results)
	}
	for _, result := range resultsToCheck {
		if result != nil && !errors.Is(result, context.Canceled) {
			return result
		}
	}
	return resultsToCheck[len(resultsToCheck)-1]
}

// synchronizeSIMIdentity detects hot-swapped cards without persisting the ICCID itself.
func synchronizeSIMIdentity(
	ctx context.Context,
	session deviceSession,
	config agent.Config,
	store identity.Store,
	state identity.State,
	synchronize func(context.Context, agent.Config, identity.State) (serverapi.SIMAssignment, error),
	logger *slog.Logger,
) error {
	interval := config.RadioStatusPoll
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	current := state
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			iccid, err := session.ReadSIMICCID(ctx)
			if err != nil {
				logger.Warn("SIM identity poll failed", "slot_key", current.SlotKey, "error", err)
				continue
			}
			fingerprint, err := current.FingerprintSIM(iccid)
			if err != nil || fingerprint == current.SIMFingerprint {
				continue
			}
			requestState := current
			requestState.SIMFingerprint = fingerprint
			assignment, err := synchronize(ctx, config, requestState)
			if err != nil {
				logger.Warn("synchronize hot-swapped SIM failed", "error", err)
				continue
			}
			updated, err := store.SetServerSIMAssignment(
				current, assignment.SlotID, fingerprint, assignment.SIMID, assignment.NumericID,
			)
			if err != nil {
				logger.Warn("persist hot-swapped SIM assignment failed", "error", err)
				continue
			}
			current = updated
			logger.Info("SIM card replacement synchronized", "slot_key", current.SlotKey, "slot_index", current.SlotIndex)
			return errSIMIdentityChanged
		}
	}
}

type callHangupper interface {
	Hangup(context.Context) error
}

func handleMediaErrors(ctx context.Context, failures <-chan error, calls callHangupper, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-failures:
			if !ok {
				return
			}
			logger.Error("call media stopped", "error", err)
			cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			hangupError := calls.Hangup(cleanupContext)
			cancel()
			if hangupError != nil {
				logger.Error("failed to hang up cellular call after media failure", "media_error", err, "error", hangupError)
			} else {
				logger.Info("cellular call hung up after media failure")
			}
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
