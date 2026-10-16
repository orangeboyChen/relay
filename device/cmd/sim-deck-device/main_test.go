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
	runError        error
	runStarted      chan struct{}
	calls           *call.Controller
}

func (session *fakeDeviceSession) Calls() *call.Controller { return session.calls }

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
		openTransport: func(string, int) (io.ReadWriteCloser, error) { return transport, nil },
		bindIdentity: func(_ context.Context, _ agent.Config, _ identity.Store, state identity.State) (identity.State, error) {
			state.NumericID = 2
			return state, nil
		},
		openInbox: func(string, time.Duration, []byte) (*sms.Inbox, error) { return sms.NewInbox(time.Hour), nil },
		newSession: func(_ *at.Client, _ *sms.Inbox, sink modem.MessageSink, _ *slog.Logger, _ time.Duration) (deviceSession, error) {
			Expect(sink.ReceiveSMS(context.Background(), sms.ReceivedMessage{ID: "message-id", Segments: 1})).To(MatchError(modem.ErrMessageDeliveryDeferred))
			return session, nil
		},
		configureAudio: func(context.Context, agent.Config, *at.Client) error { return nil },
		syncCommandKey: func(context.Context, agent.Config, identity.State) error { return nil },
		newCallEvents: func(agent.Config, identity.State, <-chan call.Snapshot) (callEventRuntime, error) {
			return eventRunner, nil
		},
		newIncomingCalls: func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar) (commandRuntime, error) {
			return &fakeCommandRuntime{}, nil
		},
		newCallMedia: func(agent.Config) (callMediaRuntime, error) { return mediaRunner, nil },
		newCommands: func(agent.Config, identity.State, command.Calls, command.Dependencies) (commandRuntime, error) {
			return commandRunner, nil
		},
		newRadioStatus: func(agent.Config, identity.State, *at.Client) (commandRuntime, error) {
			return &fakeCommandRuntime{}, nil
		},
	}
}

type testCallCommander struct{}

func (*testCallCommander) Send(context.Context, string) (at.Response, error) {
	return at.Response{Terminal: "OK"}, nil
}

var _ = Describe("Device command runtime", func() {
	var logger *slog.Logger

	BeforeEach(func() {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
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
		dependencies.openTransport = func(string, int) (io.ReadWriteCloser, error) {
			return nil, errors.New("AT device unavailable")
		}

		err := runDevice(context.Background(), testConfig(GinkgoT().TempDir()), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError("AT device unavailable"))
	})

	It("returns an identity persistence failure", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		openedTransport := false
		dependencies.openTransport = func(string, int) (io.ReadWriteCloser, error) {
			openedTransport = true
			return transport, nil
		}

		err := runDevice(context.Background(), testConfig("/dev/null/sim-deck"), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError(ContainSubstring("load device identity")))
		Expect(openedTransport).To(BeFalse())
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
		dependencies.newIncomingCalls = func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar) (commandRuntime, error) {
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
		dependencies.newIncomingCalls = func(agent.Config, identity.State, <-chan call.Snapshot, incomingcall.EventRegistrar) (commandRuntime, error) {
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

	It("logs media failures until the device stops", func() {
		var output synchronizedBuffer
		mediaLogger := slog.New(slog.NewTextHandler(&output, nil))
		failures := make(chan error, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			logMediaErrors(ctx, failures, mediaLogger)
			close(done)
		}()

		failures <- errors.New("ICE failed")
		Eventually(output.String).Should(ContainSubstring("call media stopped"))
		Eventually(output.String).Should(ContainSubstring("ICE failed"))
		cancel()
		Eventually(done).Should(BeClosed())
	})

	It("constructs all production dependency functions", func() {
		var expectedState identity.State
		httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPut))
			Expect(request.URL.String()).To(Equal("https://deck.example/v1/device-command-key"))
			Expect(request.Header.Get("Authorization")).To(HavePrefix("Bearer "))
			Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
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
		})}
		dependencies := productionDependencies(httpClient)
		Expect(dependencies.openTransport).NotTo(BeNil())
		Expect(dependencies.bindIdentity).NotTo(BeNil())
		Expect(dependencies.openInbox).NotTo(BeNil())
		Expect(dependencies.newSession).NotTo(BeNil())
		Expect(dependencies.configureAudio).NotTo(BeNil())
		Expect(dependencies.syncCommandKey).NotTo(BeNil())
		Expect(dependencies.newCallEvents).NotTo(BeNil())
		Expect(dependencies.newIncomingCalls).NotTo(BeNil())
		Expect(dependencies.newCallMedia).NotTo(BeNil())
		Expect(dependencies.newCommands).NotTo(BeNil())

		stateDirectory := GinkgoT().TempDir()
		_, err := dependencies.openTransport(filepath.Join(stateDirectory, "missing-at"), 115200)
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
		state, err = stateStore.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		state, err = stateStore.Bind(state, "https://deck.example", 1)
		Expect(err).NotTo(HaveOccurred())
		expectedState = state
		bound, err := dependencies.bindIdentity(context.Background(), config, stateStore, state)
		Expect(err).NotTo(HaveOccurred())
		Expect(bound.DeviceUUID).To(Equal(state.DeviceUUID))
		Expect(dependencies.syncCommandKey(context.Background(), config, state)).To(Succeed())
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
		incomingCalls, err := dependencies.newIncomingCalls(config, state, session.Calls().Subscribe(), events)
		Expect(err).NotTo(HaveOccurred())
		Expect(incomingCalls).NotTo(BeNil())
		callMedia, err := dependencies.newCallMedia(config)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = callMedia.Close() })
		commands, err := dependencies.newCommands(config, state, session.Calls(), command.Dependencies{Events: events, Media: callMedia})
		Expect(err).NotTo(HaveOccurred())
		Expect(commands).NotTo(BeNil())
	})
})
