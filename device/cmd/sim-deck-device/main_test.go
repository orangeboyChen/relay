package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/agent"
	"sim-deck-device/internal/at"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/modem"
	"sim-deck-device/internal/sms"
)

type blockingTransport struct {
	done      chan struct{}
	closeOnce sync.Once
}

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
		ATDevice:         "/dev/sim-deck/at",
		Baud:             115200,
		CommandTimeout:   time.Second,
		StateDirectory:   stateDirectory,
		ServerURL:        "https://deck.example",
		PairingTokenFile: filepath.Join(stateDirectory, "pairing-token"),
	}
}

func testDependencies(transport io.ReadWriteCloser, session deviceSession) deviceDependencies {
	return deviceDependencies{
		openTransport: func(string, int) (io.ReadWriteCloser, error) { return transport, nil },
		bindIdentity: func(_ context.Context, _ agent.Config, _ identity.Store, state identity.State) (identity.State, error) {
			state.NumericID = 2
			return state, nil
		},
		openInbox: func(string, time.Duration) (*sms.Inbox, error) { return sms.NewInbox(time.Hour), nil },
		newSession: func(_ *at.Client, _ *sms.Inbox, sink modem.MessageSink, _ *slog.Logger, _ time.Duration) (deviceSession, error) {
			Expect(sink.ReceiveSMS(context.Background(), sms.ReceivedMessage{ID: "message-id", Segments: 1})).To(Succeed())
			return session, nil
		},
	}
}

var _ = Describe("Device command runtime", func() {
	var logger *slog.Logger

	BeforeEach(func() {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
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

		err := runDevice(context.Background(), testConfig("/dev/null/sim-deck"), logger, &atomic.Bool{}, dependencies)

		Expect(err).To(MatchError(ContainSubstring("load device identity")))
	})

	It("returns an inbox initialization failure", func() {
		transport := newBlockingTransport()
		dependencies := testDependencies(transport, &fakeDeviceSession{})
		dependencies.openInbox = func(string, time.Duration) (*sms.Inbox, error) {
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

	It("constructs all production dependency functions", func() {
		dependencies := productionDependencies()
		Expect(dependencies.openTransport).NotTo(BeNil())
		Expect(dependencies.bindIdentity).NotTo(BeNil())
		Expect(dependencies.openInbox).NotTo(BeNil())
		Expect(dependencies.newSession).NotTo(BeNil())
	})
})
