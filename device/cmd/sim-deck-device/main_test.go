package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/agent"
	"sim-deck-device/internal/at"
	"sim-deck-device/internal/call"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/modem"
	"sim-deck-device/internal/serverapi"
	"sim-deck-device/internal/slotpolicy"
	"sim-deck-device/internal/sms"
)

type blockingTransport struct {
	done      chan struct{}
	closeOnce sync.Once
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(value)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type failingListener struct {
	err error
}

func (listener *failingListener) Accept() (net.Conn, error) { return nil, listener.err }
func (*failingListener) Close() error                       { return nil }
func (*failingListener) Addr() net.Addr                     { return testAddress("health") }

type testAddress string

func (address testAddress) Network() string { return "test" }
func (address testAddress) String() string  { return string(address) }

type blockingListener struct {
	closed    chan struct{}
	closeOnce sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{closed: make(chan struct{})}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	<-listener.closed
	return nil, net.ErrClosed
}

func (listener *blockingListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.closed) })
	return nil
}

func (*blockingListener) Addr() net.Addr { return testAddress("health") }

func newBlockingTransport() *blockingTransport {
	return &blockingTransport{done: make(chan struct{})}
}

func (transport *blockingTransport) Read([]byte) (int, error) {
	<-transport.done
	return 0, io.EOF
}

func (*blockingTransport) Write(value []byte) (int, error) {
	return len(value), nil
}

func (transport *blockingTransport) Close() error {
	transport.closeOnce.Do(func() { close(transport.done) })
	return nil
}

type fakeDeviceSession struct {
	initializeError error
	readSIMICCID    func(context.Context) (string, error)
	retryError      error
	retryMessage    *sms.ReceivedMessage
	sink            modem.MessageSink
	runError        error
	runStarted      chan struct{}
	calls           *call.Controller
}

func (session *fakeDeviceSession) Calls() *call.Controller { return session.calls }

func (session *fakeDeviceSession) ReadSIMICCID(ctx context.Context) (string, error) {
	if session.readSIMICCID != nil {
		return session.readSIMICCID(ctx)
	}
	return "", errors.New("no SIM fixture")
}

func (session *fakeDeviceSession) ReadSIMSlotStatus(context.Context) ([]modem.SIMSlotStatus, error) {
	return nil, modem.ErrSIMSlotUnsupported
}

func (*fakeDeviceSession) SendSMS(context.Context, string, string, byte) error { return nil }

func (session *fakeDeviceSession) RetryPendingSMS(ctx context.Context) error {
	if session.retryError != nil || session.retryMessage == nil {
		return session.retryError
	}
	err := session.sink.ReceiveSMS(ctx, *session.retryMessage)
	if errors.Is(err, modem.ErrMessageDeliveryDeferred) {
		return nil
	}
	return err
}

type fakeCommandRuntime struct {
	runError   error
	runStarted chan struct{}
}

type fakeCallEventRuntime struct {
	fakeCommandRuntime
}

func (*fakeCallEventRuntime) RegisterOutgoing(string, []byte) error { return nil }
func (*fakeCallEventRuntime) RegisterIncoming(string, []byte) error { return nil }
func (*fakeCallEventRuntime) Unregister(string)                     {}

type fakeCallMediaRuntime struct {
	failures   chan error
	closeError error
	closed     bool
}

type fakeMessageEventPublisher struct {
	err      error
	messages []sms.ReceivedMessage
}

type fakeSlotPolicy struct {
	settings     slotpolicy.Settings
	restoreError error
}

func (policy *fakeSlotPolicy) Current() slotpolicy.Settings  { return policy.settings }
func (policy *fakeSlotPolicy) Restore(context.Context) error { return policy.restoreError }
func (policy *fakeSlotPolicy) Configure(_ context.Context, update slotpolicy.Update) (slotpolicy.Settings, error) {
	if update.DataEnabled != nil {
		policy.settings.DataEnabled = *update.DataEnabled
	}
	if update.AirplaneModeEnabled != nil {
		policy.settings.AirplaneModeEnabled = *update.AirplaneModeEnabled
	}
	if update.IncomingCallsEnabled != nil {
		policy.settings.IncomingCallsEnabled = *update.IncomingCallsEnabled
	}
	if update.IncomingSMSEnabled != nil {
		policy.settings.IncomingSMSEnabled = *update.IncomingSMSEnabled
	}
	return policy.settings, nil
}

func (publisher *fakeMessageEventPublisher) Publish(_ context.Context, message sms.ReceivedMessage) error {
	publisher.messages = append(publisher.messages, message)
	return publisher.err
}

func (*fakeCallMediaRuntime) Negotiate(context.Context, string, string) (string, error) {
	return "answer-sdp", nil
}

func (*fakeCallMediaRuntime) Stop(string) error { return nil }

func (runtime *fakeCallMediaRuntime) Errors() <-chan error { return runtime.failures }

func (runtime *fakeCallMediaRuntime) Close() error {
	runtime.closed = true
	return runtime.closeError
}

func (runtime *fakeCommandRuntime) Run(ctx context.Context) error {
	if runtime.runStarted != nil {
		close(runtime.runStarted)
	}
	if runtime.runError != nil {
		return runtime.runError
	}
	<-ctx.Done()
	return ctx.Err()
}

func (session *fakeDeviceSession) Initialize(context.Context) error {
	return session.initializeError
}

func (session *fakeDeviceSession) Run(ctx context.Context) error {
	if session.runStarted != nil {
		close(session.runStarted)
	}
	if session.runError != nil {
		return session.runError
	}
	<-ctx.Done()
	return ctx.Err()
}

func testConfig(stateDirectory string) agent.Config {
	return agent.Config{
		DeviceUUID:         "01900000-0000-7000-8000-000000000001",
		ATDevice:           "/dev/sim-deck/at",
		AudioMode:          agent.AudioModeALSA,
		ALSACaptureDevice:  "hw:0,0",
		ALSAPlaybackDevice: "hw:0,0",
		Baud:               115200,
		CommandTimeout:     time.Second,
		CommandPoll:        time.Millisecond,
		RadioStatusPoll:    time.Millisecond,
		StateDirectory:     stateDirectory,
		ServerURL:          "https://deck.example",
		DiscoveryPoll:      time.Millisecond,
	}
}

func testDependencies(transport io.ReadWriteCloser, session deviceSession) deviceDependencies {
	commandRunner := &fakeCommandRuntime{}
	eventRunner := &fakeCallEventRuntime{}
	mediaRunner := &fakeCallMediaRuntime{failures: make(chan error, 1)}
	if fake, ok := session.(*fakeDeviceSession); ok && fake.calls == nil {
		fake.calls = call.NewController(&testCallCommander{})
	}
	return deviceDependencies{
		openTransport: func(string, int, string, string, string) (io.ReadWriteCloser, error) { return transport, nil },
		resolveHardware: func(_ context.Context, config agent.Config, _ *at.Client) (string, string, error) {
			return config.DeviceUUID, config.SlotKey, nil
		},
		bindIdentity: func(_ context.Context, _ agent.Config, _ identity.Store, state identity.State) (identity.State, error) {
			state.NumericID = 2
			return state, nil
		},
		openInbox: func(string, time.Duration, []byte) (*sms.Inbox, error) { return sms.NewInbox(time.Hour), nil },
		newSession: func(_ *at.Client, _ *sms.Inbox, sink modem.MessageSink, _ *slog.Logger, _ time.Duration) (deviceSession, error) {
			Expect(sink.ReceiveSMS(context.Background(), sms.ReceivedMessage{ID: "message-id", Segments: 1})).To(MatchError(modem.ErrMessageDeliveryDeferred))
			if fake, ok := session.(*fakeDeviceSession); ok {
				fake.sink = sink
			}
			return session, nil
		},
		configureAudio: func(context.Context, agent.Config, *at.Client) error { return nil },
		syncCommandKey: func(context.Context, agent.Config, identity.State) error { return nil },
		syncSIMIdentity: func(context.Context, agent.Config, identity.State) (serverapi.SIMAssignment, error) {
			return serverapi.SIMAssignment{
				SlotID:    "01900000-0000-7000-8000-000000000002",
				SIMID:     "01900000-0000-7000-8000-000000000003",
				NumericID: 2,
			}, nil
		},
		newCallEvents: func(agent.Config, identity.State, <-chan call.Snapshot) (callEventRuntime, error) {
			return eventRunner, nil
		},
		newIncomingCalls: func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar, incomingcall.CallRejecter, incomingcall.Policy) (commandRuntime, error) {
			return &fakeCommandRuntime{}, nil
		},
		newSlotPolicy: func(agent.Config, *at.Client) (slotPolicyRuntime, error) {
			return &fakeSlotPolicy{settings: slotpolicy.Settings{DataEnabled: true, IncomingCallsEnabled: true, IncomingSMSEnabled: true}}, nil
		},
		newCallMedia: func(agent.Config) (callMediaRuntime, error) { return mediaRunner, nil },
		newMessageEvents: func(agent.Config, identity.State) (messageEventPublisher, error) {
			return &fakeMessageEventPublisher{}, nil
		},
		newCommands: func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return commandRunner, nil
		},
		newRadioStatus: func(agent.Config, identity.State, *at.Client) (commandRuntime, error) {
			return &fakeCommandRuntime{}, nil
		},
	}
}

func configuredSIMIdentity(directory string, iccid string) (identity.Store, identity.State) {
	store := identity.NewStore(directory, "01900000-0000-7000-8000-000000000001")
	state, err := store.LoadOrCreate()
	Expect(err).NotTo(HaveOccurred())
	state, err = store.ConfigureSlot(state, "primary", 1)
	Expect(err).NotTo(HaveOccurred())
	state, err = store.PrepareDiscovery(state)
	Expect(err).NotTo(HaveOccurred())
	state, err = store.BindAssignment(
		state,
		"https://deck.example",
		2,
		"01900000-0000-7000-8000-000000000002",
		"01900000-0000-7000-8000-000000000003",
		"primary",
		1,
	)
	Expect(err).NotTo(HaveOccurred())
	fingerprint, err := state.FingerprintSIM(iccid)
	Expect(err).NotTo(HaveOccurred())
	state, err = store.SetSIMIdentity(state, fingerprint, state.SIMID, state.NumericID)
	Expect(err).NotTo(HaveOccurred())
	return store, state
}

type testCallCommander struct{}

func (*testCallCommander) Send(context.Context, string) (at.Response, error) {
	return at.Response{Terminal: "OK"}, nil
}

type recordingCallHangupper struct {
	called chan struct{}
	err    error
}

func (calls *recordingCallHangupper) Hangup(context.Context) error {
	calls.called <- struct{}{}
	return calls.err
}

var _ = Describe("Device command runtime", func() {
	var logger *slog.Logger

	BeforeEach(func() {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	})

	It("wires the production runtime and reports an unavailable AT device", func() {
		config := testConfig(GinkgoT().TempDir())
		config.HealthAddress = "127.0.0.1:0"
		config.ATDevice = filepath.Join(GinkgoT().TempDir(), "missing-at")

		err := run(context.Background(), config, logger)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("device runtime"))
	})

	It("fails before starting the device when the health address cannot bind", func() {
		deviceStarted := false
		dependencies := runtimeDependencies{
			listen: func(context.Context, string, string) (net.Listener, error) {
				return nil, errors.New("address already in use")
			},
			runDevice: func(context.Context, agent.Config, *slog.Logger, *atomic.Bool, deviceDependencies) error {
				deviceStarted = true
				return nil
			},
		}

		err := runWithDependencies(context.Background(), testConfig(GinkgoT().TempDir()), logger, dependencies)

		Expect(err).To(MatchError("listen for health checks: address already in use"))
		Expect(deviceStarted).To(BeFalse())
	})

	It("stops and awaits the device when the health server fails", func() {
		healthFailure := errors.New("health listener failed")
		deviceCanceled := make(chan struct{})
		releaseDevice := make(chan struct{})
		result := make(chan error, 1)
		dependencies := runtimeDependencies{
			listen: func(context.Context, string, string) (net.Listener, error) {
				return &failingListener{err: healthFailure}, nil
			},
			runDevice: func(ctx context.Context, _ agent.Config, _ *slog.Logger, _ *atomic.Bool, _ deviceDependencies) error {
				<-ctx.Done()
				close(deviceCanceled)
				<-releaseDevice
				return ctx.Err()
			},
		}

		go func() {
			result <- runWithDependencies(context.Background(), testConfig(GinkgoT().TempDir()), logger, dependencies)
		}()

		Eventually(deviceCanceled).Should(BeClosed())
		Consistently(result).ShouldNot(Receive())
		close(releaseDevice)
		Eventually(result).Should(Receive(MatchError("health server: health listener failed")))
	})

	It("shuts down the health listener after device cancellation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		deviceStarted := make(chan struct{})
		listener := newBlockingListener()
		dependencies := runtimeDependencies{
			listen: func(context.Context, string, string) (net.Listener, error) {
				return listener, nil
			},
			runDevice: func(ctx context.Context, _ agent.Config, _ *slog.Logger, _ *atomic.Bool, _ deviceDependencies) error {
				close(deviceStarted)
				<-ctx.Done()
				return ctx.Err()
			},
		}
		result := make(chan error, 1)

		go func() {
			result <- runWithDependencies(ctx, testConfig(GinkgoT().TempDir()), logger, dependencies)
		}()
		Eventually(deviceStarted).Should(BeClosed())
		cancel()
		Eventually(result).Should(Receive(BeNil()))
		Expect(listener.closed).To(BeClosed())
	})

	It("returns after a successful device runtime exit", func() {
		listener := newBlockingListener()
		dependencies := runtimeDependencies{
			listen: func(context.Context, string, string) (net.Listener, error) {
				return listener, nil
			},
			runDevice: func(context.Context, agent.Config, *slog.Logger, *atomic.Bool, deviceDependencies) error {
				return nil
			},
		}

		result := make(chan error, 1)
		go func() {
			result <- runWithDependencies(context.Background(), testConfig(GinkgoT().TempDir()), logger, dependencies)
		}()

		Eventually(result).Should(Receive(BeNil()))
	})

	It("sets readiness only for the initialized and bound session lifetime", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{runStarted: make(chan struct{})}
		ready := &atomic.Bool{}
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)

		go func() {
			result <- runDevice(ctx, testConfig(GinkgoT().TempDir()), logger, ready, testDependencies(transport, session))
		}()

		Eventually(session.runStarted).Should(BeClosed())
		Expect(ready.Load()).To(BeTrue())
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
		Expect(ready.Load()).To(BeFalse())
	})

	It("clears readiness when the running modem session fails", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{runError: errors.New("serial failed")}
		ready := &atomic.Bool{}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, ready, testDependencies(transport, session))

		Expect(err).To(MatchError("serial failed"))
		Expect(ready.Load()).To(BeFalse())
	})

	It("returns an AT transport open failure", func() {
		dependencies := testDependencies(newBlockingTransport(), &fakeDeviceSession{})
		dependencies.openTransport = func(string, int, string, string, string) (io.ReadWriteCloser, error) {
			return nil, errors.New("AT device unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("AT device unavailable"))
	})

	It("returns an identity persistence failure", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		openedTransport := false
		dependencies.openTransport = func(string, int, string, string, string) (io.ReadWriteCloser, error) {
			openedTransport = true
			return transport, nil
		}

		err := runDevice(context.Background(), testConfig("/dev/null/sim-deck"), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError(ContainSubstring("load device identity")))
		Expect(openedTransport).To(BeTrue())
	})

	It("returns an inbox initialization failure", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.openInbox = func(string, time.Duration, []byte) (*sms.Inbox, error) {
			return nil, errors.New("inbox unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("open SMS inbox: inbox unavailable"))
	})

	It("returns a modem session construction failure", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newSession = func(*at.Client, *sms.Inbox, modem.MessageSink, *slog.Logger, time.Duration) (deviceSession, error) {
			return nil, errors.New("session unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("session unavailable"))
	})

	It("does not bind when modem initialization fails", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{initializeError: errors.New("modem rejected setup")}
		dependencies := testDependencies(transport, session)
		bound := false
		dependencies.bindIdentity = func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error) {
			bound = true
			return identity.State{}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("modem rejected setup"))
		Expect(bound).To(BeFalse())
	})

	It("does not bind when modem audio configuration fails", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.configureAudio = func(context.Context, agent.Config, *at.Client) error {
			return errors.New("UAC unavailable")
		}
		bound := false
		dependencies.bindIdentity = func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error) {
			bound = true
			return identity.State{}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("configure modem audio: UAC unavailable"))
		Expect(bound).To(BeFalse())
	})

	It("returns a server binding failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.bindIdentity = func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error) {
			return identity.State{}, errors.New("pairing unavailable")
		}
		ready := &atomic.Bool{}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, ready, dependencies)

		Expect(err).To(MatchError("bind device to server: pairing unavailable"))
		Expect(ready.Load()).To(BeFalse())
	})

	It("returns a command runtime construction failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newCommands = func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return nil, errors.New("command runtime unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("create encrypted command runtime: command runtime unavailable"))
	})

	It("returns a radio runtime construction failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newRadioStatus = func(agent.Config, identity.State, *at.Client) (commandRuntime, error) {
			return nil, errors.New("radio unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("create radio status runtime: radio unavailable"))
	})

	It("returns a call event runtime construction failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newCallEvents = func(agent.Config, identity.State, <-chan call.Snapshot) (callEventRuntime, error) {
			return nil, errors.New("event runtime unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("create encrypted call event runtime: event runtime unavailable"))
	})

	It("returns an incoming call runtime construction failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newIncomingCalls = func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar, incomingcall.CallRejecter, incomingcall.Policy) (commandRuntime, error) {
			return nil, errors.New("incoming call runtime unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("create incoming call runtime: incoming call runtime unavailable"))
	})

	It("returns a call media runtime construction failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newCallMedia = func(agent.Config) (callMediaRuntime, error) {
			return nil, errors.New("audio unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("create call media runtime: audio unavailable"))
	})

	It("returns command key registration failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.syncCommandKey = func(context.Context, agent.Config, identity.State) error {
			return errors.New("key registration unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("register device command key: key registration unavailable"))
	})

	It("requires the SIM synchronization dependency before becoming ready", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.syncSIMIdentity = nil

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("SIM identity synchronization is not configured"))
	})

	It("synchronizes the detected SIM before constructing event publishers", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{readSIMICCID: func(context.Context) (string, error) {
			return "8986000000000000000", nil
		}}
		dependencies := testDependencies(transport, session)
		dependencies.bindIdentity = func(_ context.Context, config agent.Config, store identity.Store, state identity.State) (identity.State, error) {
			prepared, err := store.PrepareDiscovery(state)
			if err != nil {
				return identity.State{}, err
			}
			return store.BindAssignment(
				prepared,
				config.ServerURL,
				2,
				"01900000-0000-7000-8000-000000000002",
				"01900000-0000-7000-8000-000000000003",
				config.SlotKey,
				config.SlotIndex,
			)
		}
		const synchronizedSlotID = "01900000-0000-7000-8000-000000000005"
		const synchronizedSIMID = "01900000-0000-7000-8000-000000000004"
		dependencies.syncSIMIdentity = func(_ context.Context, _ agent.Config, state identity.State) (serverapi.SIMAssignment, error) {
			Expect(state.SIMFingerprint).To(HaveLen(64))
			Expect(state.SlotID).To(Equal("01900000-0000-7000-8000-000000000002"))
			return serverapi.SIMAssignment{SlotID: synchronizedSlotID, SIMID: synchronizedSIMID, NumericID: 4}, nil
		}
		var publisherState identity.State
		dependencies.newMessageEvents = func(_ agent.Config, state identity.State) (messageEventPublisher, error) {
			publisherState = state
			return &fakeMessageEventPublisher{}, nil
		}
		dependencies.newCommands = func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return &fakeCommandRuntime{runError: errors.New("stop after SIM synchronization")}, nil
		}
		stateDirectory := GinkgoT().TempDir()

		err := runDevice(context.Background(), testConfig(stateDirectory), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("stop after SIM synchronization"))
		Expect(publisherState.SlotID).To(Equal(synchronizedSlotID))
		Expect(publisherState.SIMID).To(Equal(synchronizedSIMID))
		Expect(publisherState.NumericID).To(Equal(4))
		reloaded, reloadErr := identity.NewStore(stateDirectory, testConfig(stateDirectory).DeviceUUID).LoadOrCreate()
		Expect(reloadErr).NotTo(HaveOccurred())
		Expect(reloaded.SlotID).To(Equal(synchronizedSlotID))
		Expect(reloaded.SIMID).To(Equal(synchronizedSIMID))
		Expect(reloaded.SIMFingerprint).To(HaveLen(64))
	})

	It("stops before readiness when initial SIM synchronization fails", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{readSIMICCID: func(context.Context) (string, error) {
			return "8986000000000000000", nil
		}}
		dependencies := testDependencies(transport, session)
		dependencies.bindIdentity = func(_ context.Context, config agent.Config, store identity.Store, state identity.State) (identity.State, error) {
			prepared, err := store.PrepareDiscovery(state)
			if err != nil {
				return identity.State{}, err
			}
			return store.BindAssignment(
				prepared,
				config.ServerURL,
				2,
				"01900000-0000-7000-8000-000000000002",
				"01900000-0000-7000-8000-000000000003",
				config.SlotKey,
				config.SlotIndex,
			)
		}
		dependencies.syncSIMIdentity = func(context.Context, agent.Config, identity.State) (serverapi.SIMAssignment, error) {
			return serverapi.SIMAssignment{}, errors.New("SIM endpoint unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("synchronize current SIM identity: SIM endpoint unavailable"))
	})

	It("rejects a malformed ICCID before contacting the server", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{readSIMICCID: func(context.Context) (string, error) {
			return "invalid-iccid", nil
		}}
		dependencies := testDependencies(transport, session)
		bound := false
		dependencies.bindIdentity = func(context.Context, agent.Config, identity.Store, identity.State) (identity.State, error) {
			bound = true
			return identity.State{}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError(ContainSubstring("fingerprint SIM identity")))
		Expect(bound).To(BeFalse())
	})

	It("keeps polling when the SIM card is unchanged and stops on cancellation", func() {
		const iccid = "8986000000000000000"
		store, state := configuredSIMIdentity(GinkgoT().TempDir(), iccid)
		var polls atomic.Int32
		session := &fakeDeviceSession{readSIMICCID: func(context.Context) (string, error) {
			polls.Add(1)
			return iccid, nil
		}}
		var synchronizations atomic.Int32
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		config := testConfig(GinkgoT().TempDir())

		go func() {
			result <- synchronizeSIMIdentity(ctx, session, config, store, state, func(context.Context, agent.Config, identity.State) (serverapi.SIMAssignment, error) {
				synchronizations.Add(1)
				return serverapi.SIMAssignment{}, errors.New("unexpected synchronization")
			}, logger)
		}()

		Eventually(polls.Load).Should(BeNumerically(">=", 2))
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
		Expect(synchronizations.Load()).To(BeZero())
	})

	It("retries SIM reads and server synchronization before atomically replacing the assignment", func() {
		const oldICCID = "8986000000000000000"
		const newICCID = "8986000000000000001"
		const newSlotID = "01900000-0000-7000-8000-000000000005"
		const newSIMID = "01900000-0000-7000-8000-000000000004"
		store, state := configuredSIMIdentity(GinkgoT().TempDir(), oldICCID)
		var polls atomic.Int32
		session := &fakeDeviceSession{readSIMICCID: func(context.Context) (string, error) {
			switch polls.Add(1) {
			case 1:
				return "", errors.New("SIM temporarily unavailable")
			case 2:
				return "malformed", nil
			default:
				return newICCID, nil
			}
		}}
		var synchronizations atomic.Int32
		config := testConfig(GinkgoT().TempDir())

		err := synchronizeSIMIdentity(context.Background(), session, config, store, state, func(_ context.Context, received agent.Config, request identity.State) (serverapi.SIMAssignment, error) {
			Expect(received.ServerURL).To(Equal(config.ServerURL))
			Expect(request.SIMFingerprint).NotTo(Equal(state.SIMFingerprint))
			if synchronizations.Add(1) == 1 {
				return serverapi.SIMAssignment{}, errors.New("server temporarily unavailable")
			}
			return serverapi.SIMAssignment{SlotID: newSlotID, SIMID: newSIMID, NumericID: 4}, nil
		}, logger)

		Expect(err).To(MatchError(errSIMIdentityChanged))
		Expect(synchronizations.Load()).To(Equal(int32(2)))
		reloaded, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		expectedFingerprint, err := state.FingerprintSIM(newICCID)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.SlotID).To(Equal(newSlotID))
		Expect(reloaded.SIMFingerprint).To(Equal(expectedFingerprint))
		Expect(reloaded.SIMID).To(Equal(newSIMID))
		Expect(reloaded.NumericID).To(Equal(4))
	})

	It("does not persist a new fingerprint when a synchronized assignment is invalid", func() {
		const oldICCID = "8986000000000000000"
		const newICCID = "8986000000000000001"
		store, state := configuredSIMIdentity(GinkgoT().TempDir(), oldICCID)
		session := &fakeDeviceSession{readSIMICCID: func(context.Context) (string, error) {
			return newICCID, nil
		}}
		ctx, cancel := context.WithCancel(context.Background())
		var synchronizations atomic.Int32
		result := make(chan error, 1)
		config := testConfig(GinkgoT().TempDir())

		go func() {
			result <- synchronizeSIMIdentity(ctx, session, config, store, state, func(context.Context, agent.Config, identity.State) (serverapi.SIMAssignment, error) {
				if synchronizations.Add(1) >= 2 {
					cancel()
				}
				return serverapi.SIMAssignment{SlotID: state.SlotID, SIMID: "invalid-sim-id", NumericID: 9}, nil
			}, logger)
		}()

		Eventually(result).Should(Receive(MatchError(context.Canceled)))
		reloaded, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.SIMFingerprint).To(Equal(state.SIMFingerprint))
		Expect(reloaded.SIMID).To(Equal(state.SIMID))
		Expect(reloaded.NumericID).To(Equal(state.NumericID))
	})

	It("returns message event publisher construction failure before readiness", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.newMessageEvents = func(agent.Config, identity.State) (messageEventPublisher, error) {
			return nil, errors.New("message encryption unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("create encrypted message event publisher: message encryption unavailable"))
	})

	It("returns a pending SMS synchronization failure before readiness", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{retryError: errors.New("inbox unavailable")}
		dependencies := testDependencies(transport, session)

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("synchronize pending SMS: inbox unavailable"))
	})

	It("publishes pending SMS after device binding before becoming ready", func() {
		transport := newBlockingTransport()
		message := sms.ReceivedMessage{ID: "message-id", Sender: "10086", Text: "pending", Segments: 1}
		session := &fakeDeviceSession{retryMessage: &message}
		dependencies := testDependencies(transport, session)
		publisher := &fakeMessageEventPublisher{}
		dependencies.newMessageEvents = func(agent.Config, identity.State) (messageEventPublisher, error) {
			return publisher, nil
		}
		dependencies.newCommands = func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return &fakeCommandRuntime{runError: errors.New("stop after synchronization")}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("stop after synchronization"))
		Expect(publisher.messages).To(Equal([]sms.ReceivedMessage{message}))
	})

	It("retains pending SMS while incoming SMS is disabled", func() {
		transport := newBlockingTransport()
		message := sms.ReceivedMessage{ID: "message-id", Sender: "10086", Text: "pending", Segments: 1}
		session := &fakeDeviceSession{retryMessage: &message}
		dependencies := testDependencies(transport, session)
		publisher := &fakeMessageEventPublisher{}
		dependencies.newSlotPolicy = func(agent.Config, *at.Client) (slotPolicyRuntime, error) {
			return &fakeSlotPolicy{settings: slotpolicy.Settings{DataEnabled: true, IncomingCallsEnabled: true, IncomingSMSEnabled: false}}, nil
		}
		dependencies.newMessageEvents = func(agent.Config, identity.State) (messageEventPublisher, error) {
			return publisher, nil
		}
		dependencies.newCommands = func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return &fakeCommandRuntime{runError: errors.New("stop after synchronization")}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("stop after synchronization"))
		Expect(publisher.messages).To(BeEmpty())
	})

	It("keeps pending SMS durable when encrypted event upload is unavailable", func() {
		transport := newBlockingTransport()
		message := sms.ReceivedMessage{ID: "message-id", Sender: "10086", Text: "pending", Segments: 1}
		session := &fakeDeviceSession{retryMessage: &message}
		dependencies := testDependencies(transport, session)
		publisher := &fakeMessageEventPublisher{err: errors.New("event server unavailable")}
		dependencies.newMessageEvents = func(agent.Config, identity.State) (messageEventPublisher, error) {
			return publisher, nil
		}
		dependencies.newCommands = func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return &fakeCommandRuntime{runError: errors.New("stop after synchronization")}, nil
		}
		var output synchronizedBuffer
		logger = slog.New(slog.NewTextHandler(&output, nil))

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("stop after synchronization"))
		Expect(publisher.messages).To(Equal([]sms.ReceivedMessage{message}))
		Expect(output.String()).To(ContainSubstring("SMS retained pending encrypted synchronization"))
	})

	It("stops the modem session when encrypted command delivery fails", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{runStarted: make(chan struct{})}
		dependencies := testDependencies(transport, session)
		dependencies.newCommands = func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return &fakeCommandRuntime{runError: errors.New("command server unavailable")}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("command server unavailable"))
		Expect(session.runStarted).To(BeClosed())
	})

	It("stops the device when encrypted event delivery fails", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{runStarted: make(chan struct{})}
		dependencies := testDependencies(transport, session)
		dependencies.newCallEvents = func(agent.Config, identity.State, <-chan call.Snapshot) (callEventRuntime, error) {
			return &fakeCallEventRuntime{fakeCommandRuntime{runError: errors.New("event server unavailable")}}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("event server unavailable"))
		Expect(session.runStarted).To(BeClosed())
	})

	It("stops the device when incoming call delivery fails", func() {
		transport := newBlockingTransport()
		session := &fakeDeviceSession{runStarted: make(chan struct{})}
		dependencies := testDependencies(transport, session)
		dependencies.newIncomingCalls = func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar, incomingcall.CallRejecter, incomingcall.Policy) (commandRuntime, error) {
			return &fakeCommandRuntime{runError: errors.New("incoming call server unavailable")}, nil
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("incoming call server unavailable"))
		Expect(session.runStarted).To(BeClosed())
	})

	It("serves readiness as JSON", func() {
		ready := &atomic.Bool{}
		server := healthServer("unused", ready)

		request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		recorder := httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(recorder.Body.String()).To(MatchJSON(`{"ready":false}`))

		ready.Store(true)
		recorder = httptest.NewRecorder()
		server.Handler.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		Expect(recorder.Body.String()).To(MatchJSON(`{"ready":true}`))
	})

	It("hangs up the cellular call after a terminal media failure", func() {
		var output synchronizedBuffer
		mediaLogger := slog.New(slog.NewTextHandler(&output, nil))
		failures := make(chan error, 1)
		calls := &recordingCallHangupper{called: make(chan struct{}, 1)}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			handleMediaErrors(ctx, failures, calls, mediaLogger)
			close(done)
		}()

		failures <- errors.New("ICE failed")
		Eventually(calls.called).Should(Receive())
		Eventually(output.String).Should(ContainSubstring("call media stopped"))
		Eventually(output.String).Should(ContainSubstring("ICE failed"))
		Eventually(output.String).Should(ContainSubstring("cellular call hung up after media failure"))
		cancel()
		Eventually(done).Should(BeClosed())
	})

	It("constructs all production dependency functions", func() {
		var expectedState identity.State
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPut))
			Expect(request.Header.Get("Authorization")).To(HavePrefix("Bearer "))
			Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
			switch request.URL.Path {
			case "/v1/device-command-key":
				Expect(request.Header.Get("Authorization")).To(HavePrefix("Bearer "))
				body, err := io.ReadAll(request.Body)
				Expect(err).NotTo(HaveOccurred())
				var payload struct {
					DeviceUUID       string `json:"device_uuid"`
					CommandPublicKey []byte `json:"command_public_key"`
				}
				Expect(json.Unmarshal(body, &payload)).To(Succeed())
				Expect(payload.DeviceUUID).To(Equal(expectedState.DeviceUUID))
				Expect(payload.CommandPublicKey).To(Equal(expectedState.CommandPublicKey()))
				return &http.Response{
					StatusCode: http.StatusNoContent,
					Body:       io.NopCloser(strings.NewReader("")),
					Header:     make(http.Header),
				}, nil
			case "/v1/device-sim-identity":
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"slot_id":"01900000-0000-7000-8000-000000000002","sim_id":"01900000-0000-7000-8000-000000000003","numeric_id":2}`,
					)),
					Header: make(http.Header),
				}, nil
			default:
				Fail("unexpected production dependency request: " + request.URL.String())
				return nil, nil
			}
		})}
		dependencies := productionDependencies(httpClient)
		Expect(dependencies.openTransport).NotTo(BeNil())
		Expect(dependencies.bindIdentity).NotTo(BeNil())
		Expect(dependencies.openInbox).NotTo(BeNil())
		Expect(dependencies.newSession).NotTo(BeNil())
		Expect(dependencies.configureAudio).NotTo(BeNil())
		Expect(dependencies.syncCommandKey).NotTo(BeNil())
		Expect(dependencies.syncSIMIdentity).NotTo(BeNil())
		Expect(dependencies.newCallEvents).NotTo(BeNil())
		Expect(dependencies.newIncomingCalls).NotTo(BeNil())
		Expect(dependencies.newCallMedia).NotTo(BeNil())
		Expect(dependencies.newMessageEvents).NotTo(BeNil())
		Expect(dependencies.newCommands).NotTo(BeNil())

		stateDirectory := GinkgoT().TempDir()
		_, err := dependencies.openTransport(filepath.Join(stateDirectory, "missing-at"), 115200, "", "", "")
		Expect(err).To(HaveOccurred())
		_, err = dependencies.openInbox(
			filepath.Join(stateDirectory, "wired-inbox.json"),
			time.Hour,
			bytes.Repeat([]byte{1}, 32),
		)
		Expect(err).NotTo(HaveOccurred())
		config := testConfig(stateDirectory)
		config.ServerURL = "https://deck.example"
		stateStore := identity.NewStore(stateDirectory, config.DeviceUUID)
		state, err := stateStore.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		state, err = stateStore.ConfigureSlot(state, "primary", 1)
		Expect(err).NotTo(HaveOccurred())
		fingerprint, err := state.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())
		state, err = stateStore.SetSIMFingerprint(state, fingerprint)
		Expect(err).NotTo(HaveOccurred())
		state, err = stateStore.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		state, err = stateStore.BindAssignment(
			state,
			"https://deck.example",
			2,
			"01900000-0000-7000-8000-000000000002",
			"01900000-0000-7000-8000-000000000003",
			"primary",
			1,
		)
		Expect(err).NotTo(HaveOccurred())
		expectedState = state
		bound, err := dependencies.bindIdentity(context.Background(), config, stateStore, state)
		Expect(err).NotTo(HaveOccurred())
		Expect(bound.DeviceUUID).To(Equal(state.DeviceUUID))
		Expect(dependencies.syncCommandKey(context.Background(), config, state)).To(Succeed())
		assignment, err := dependencies.syncSIMIdentity(context.Background(), config, state)
		Expect(err).NotTo(HaveOccurred())
		Expect(assignment.SlotID).To(Equal(state.SlotID))
		Expect(assignment.SIMID).To(Equal(state.SIMID))
		Expect(assignment.NumericID).To(Equal(state.NumericID))
		transport := newBlockingTransport()
		client := at.NewClient(transport)
		DeferCleanup(func() { _ = client.Close() })
		session, err := dependencies.newSession(
			client,
			sms.NewInbox(time.Hour),
			modem.MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }),
			logger,
			time.Second,
		)
		Expect(err).NotTo(HaveOccurred())
		events, err := dependencies.newCallEvents(config, state, session.Calls().Events())
		Expect(err).NotTo(HaveOccurred())
		policy, err := dependencies.newSlotPolicy(config, client)
		Expect(err).NotTo(HaveOccurred())
		incomingCalls, err := dependencies.newIncomingCalls(config, state, session.Calls().Subscribe(), events, session.Calls(), policy)
		Expect(err).NotTo(HaveOccurred())
		Expect(incomingCalls).NotTo(BeNil())
		callMedia, err := dependencies.newCallMedia(config)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = callMedia.Close() })
		messageEvents, err := dependencies.newMessageEvents(config, state)
		Expect(err).NotTo(HaveOccurred())
		Expect(messageEvents).NotTo(BeNil())
		commands, err := dependencies.newCommands(config, state, session.Calls(), command.Dependencies{Events: events, Media: callMedia, Messages: session, SlotSettings: policy})
		Expect(err).NotTo(HaveOccurred())
		Expect(commands).NotTo(BeNil())
		radioStatus, err := dependencies.newRadioStatus(config, state, client)
		Expect(err).NotTo(HaveOccurred())
		Expect(radioStatus).NotTo(BeNil())
	})
})
