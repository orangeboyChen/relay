package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/apns"
	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/bootstrap"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/push"
	"sim-deck-server/internal/store"
)

type memoryRepository struct {
	migrateError error
	closed       bool
}

func (repository *memoryRepository) Migrate(context.Context) error { return repository.migrateError }
func (repository *memoryRepository) Close()                        { repository.closed = true }
func (repository *memoryRepository) OwnerExists(context.Context) (bool, error) {
	return false, nil
}
func (repository *memoryRepository) CreateOwner(context.Context, string, []byte) (bool, error) {
	return true, nil
}
func (repository *memoryRepository) ListDevices(context.Context) ([]store.DeviceRecord, error) {
	return nil, nil
}
func (repository *memoryRepository) UpdateNumericID(context.Context, string, int) error {
	return nil
}
func (repository *memoryRepository) UpdateDeviceProfile(context.Context, string, []byte) error {
	return nil
}
func (repository *memoryRepository) RevokeDevice(context.Context, string, time.Time) error {
	return nil
}
func (repository *memoryRepository) InsertEvent(context.Context, domain.EventEnvelope, time.Time) (int64, bool, error) {
	return 0, false, nil
}
func (repository *memoryRepository) ListEvents(context.Context, int64, int, time.Time) ([]domain.EventEnvelope, error) {
	return nil, nil
}
func (repository *memoryRepository) OwnerCredentialByUsername(context.Context, string) (store.OwnerCredential, error) {
	return store.OwnerCredential{}, pgx.ErrNoRows
}
func (repository *memoryRepository) CreateSession(context.Context, string, auth.TokenPair) error {
	return nil
}
func (repository *memoryRepository) RotateSession(context.Context, string, auth.TokenPair, time.Time) error {
	return nil
}
func (repository *memoryRepository) RevokeSession(context.Context, string, time.Time) error {
	return nil
}
func (repository *memoryRepository) AuthenticateOwnerSession(context.Context, string, time.Time) (string, error) {
	return "owner-1", nil
}
func (repository *memoryRepository) CreatePairingToken(context.Context, string, []byte, time.Time) error {
	return nil
}
func (repository *memoryRepository) ConsumePairingToken(context.Context, []byte, time.Time, store.DevicePairing) (store.DeviceRecord, error) {
	return store.DeviceRecord{}, nil
}
func (repository *memoryRepository) AuthenticateDevice(context.Context, string, string, time.Time) ([]byte, error) {
	return make([]byte, ed25519.PublicKeySize), nil
}
func (repository *memoryRepository) EnqueueCommand(context.Context, domain.EncryptedCommand) (bool, error) {
	return true, nil
}
func (repository *memoryRepository) ClaimCommand(context.Context, string, time.Time, time.Time) (domain.EncryptedCommand, error) {
	return domain.EncryptedCommand{}, pgx.ErrNoRows
}
func (repository *memoryRepository) CompleteCommand(context.Context, string, string, domain.EncryptedCommandResult, time.Time) error {
	return nil
}
func (repository *memoryRepository) CommandStatus(context.Context, string, string, time.Time) (domain.CommandStatus, error) {
	return domain.CommandStatus{}, pgx.ErrNoRows
}
func (repository *memoryRepository) UpdateDeviceCommandPublicKey(context.Context, string, []byte) error {
	return nil
}
func (repository *memoryRepository) UpdateDeviceRadioStatus(context.Context, string, string, *int, time.Time) error {
	return nil
}
func (repository *memoryRepository) RegisterAppInstallation(_ context.Context, _ string, registration store.AppInstallationRegistration, now time.Time) (store.AppInstallationRecord, bool, error) {
	approvedAt := now.UTC()
	return store.AppInstallationRecord{
		ID:                    registration.ID,
		IdentityPublicKey:     append([]byte(nil), registration.IdentityPublicKey...),
		KeyAgreementPublicKey: append([]byte(nil), registration.KeyAgreementPublicKey...),
		WrappedVaultKey:       append([]byte(nil), registration.WrappedVaultKey...),
		ApprovedAt:            &approvedAt,
		CreatedAt:             approvedAt,
	}, true, nil
}

func (repository *memoryRepository) UpsertPushToken(
	context.Context,
	string,
	string,
	string,
	string,
	[]byte,
	time.Time,
) error {
	return nil
}
func (repository *memoryRepository) DeletePushToken(context.Context, string, string, string, string) error {
	return nil
}
func (repository *memoryRepository) ListApprovedAppInstallationRecipients(context.Context) ([]store.AppInstallationRecipient, error) {
	return nil, nil
}
func (repository *memoryRepository) InsertIncomingCall(context.Context, domain.IncomingCallNotification, time.Time) (bool, error) {
	return true, nil
}
func (repository *memoryRepository) ClaimIncomingCall(context.Context, string, string, string, time.Time) (domain.IncomingCallClaim, error) {
	return domain.IncomingCallClaim{}, pgx.ErrNoRows
}
func (repository *memoryRepository) ClaimVoIPPushJob(context.Context, time.Time, time.Duration) (store.OutboxPushJob, error) {
	return store.OutboxPushJob{}, pgx.ErrNoRows
}
func (repository *memoryRepository) CompletePushJob(context.Context, int64, int, time.Time) error {
	return nil
}
func (repository *memoryRepository) DiscardPushJob(context.Context, int64, int, time.Time, string) error {
	return nil
}
func (repository *memoryRepository) RetryPushJob(context.Context, int64, int, time.Time, string) error {
	return nil
}
func (repository *memoryRepository) DeleteVoIPPushToken(context.Context, string, string, []byte) error {
	return nil
}

type memoryHTTPServer struct {
	listenError   error
	shutdownError error
	started       chan struct{}
	release       chan struct{}
	startOnce     sync.Once
	plainCalls    int
	tlsCalls      int
	certificate   string
	privateKey    string
	shutdownCalls int
}

func newMemoryHTTPServer() *memoryHTTPServer {
	return &memoryHTTPServer{started: make(chan struct{}), release: make(chan struct{})}
}

func (server *memoryHTTPServer) ListenAndServe() error {
	server.plainCalls++
	server.startOnce.Do(func() { close(server.started) })
	if server.listenError != nil {
		return server.listenError
	}
	<-server.release
	return http.ErrServerClosed
}

func (server *memoryHTTPServer) ListenAndServeTLS(certificate string, privateKey string) error {
	server.tlsCalls++
	server.certificate = certificate
	server.privateKey = privateKey
	server.startOnce.Do(func() { close(server.started) })
	if server.listenError != nil {
		return server.listenError
	}
	<-server.release
	return http.ErrServerClosed
}

func (server *memoryHTTPServer) Shutdown(context.Context) error {
	server.shutdownCalls++
	server.startOnce.Do(func() { close(server.started) })
	select {
	case <-server.release:
	default:
		close(server.release)
	}
	return server.shutdownError
}

type memoryWorker struct {
	started chan struct{}
	err     error
}

func (worker *memoryWorker) Run(ctx context.Context) error {
	close(worker.started)
	if worker.err != nil {
		return worker.err
	}
	<-ctx.Done()
	return nil
}

var _ = Describe("Server command", func() {
	var (
		environment       map[string]string
		memoryStore       *memoryRepository
		server            *memoryHTTPServer
		openError         error
		bootstrapError    error
		bootstrapResult   bootstrap.Result
		openedDatabaseURL string
		serverAddress     string
		serverHandler     http.Handler
		pushWorker        *memoryWorker
		pushConfig        apns.Config
		pushConfigError   error
		privateKeyPath    string
		testDependencies  dependencies
	)

	BeforeEach(func() {
		environment = map[string]string{"DATABASE_URL": "postgres://memory"}
		memoryStore = &memoryRepository{}
		server = newMemoryHTTPServer()
		openError = nil
		bootstrapError = nil
		bootstrapResult = bootstrap.Result{}
		openedDatabaseURL = ""
		serverAddress = ""
		serverHandler = nil
		pushWorker = &memoryWorker{started: make(chan struct{})}
		pushConfig = apns.Config{}
		pushConfigError = nil
		privateKeyPath = ""
		testDependencies = dependencies{
			getenv: func(name string) string { return environment[name] },
			readFile: func(path string) ([]byte, error) {
				privateKeyPath = path
				return []byte("private-key"), nil
			},
			openRepository: func(_ context.Context, databaseURL string) (repository, error) {
				openedDatabaseURL = databaseURL
				return memoryStore, openError
			},
			bootstrapOwner: func(_ context.Context, _ bootstrap.OwnerStore, config bootstrap.Config) (bootstrap.Result, error) {
				Expect(config.PasswordFile).To(Equal("/run/secrets/sim_deck_admin_password"))
				Expect(config.RecoveryPublicKeyFile).To(Equal("/run/secrets/sim_deck_recovery_public_key"))
				return bootstrapResult, bootstrapError
			},
			newHTTPServer: func(address string, handler http.Handler) httpServer {
				serverAddress = address
				serverHandler = handler
				return server
			},
			newPushWorker: func(_ push.Repository, config apns.Config) (backgroundWorker, error) {
				pushConfig = config
				return pushWorker, pushConfigError
			},
		}
	})

	It("requires a database URL before opening storage", func() {
		delete(environment, "DATABASE_URL")

		err := run(context.Background(), testDependencies)

		Expect(err).To(MatchError("DATABASE_URL is required"))
		Expect(openedDatabaseURL).To(BeEmpty())
	})

	It("propagates repository open and migration failures", func() {
		openError = errors.New("open failed")
		Expect(run(context.Background(), testDependencies)).To(MatchError("open failed"))

		openError = nil
		memoryStore.migrateError = errors.New("migration failed")
		Expect(run(context.Background(), testDependencies)).To(MatchError("migration failed"))
		Expect(memoryStore.closed).To(BeTrue())
	})

	It("wraps owner bootstrap failures and closes storage", func() {
		bootstrapError = errors.New("invalid recovery key")

		err := run(context.Background(), testDependencies)

		Expect(err).To(MatchError("bootstrap owner: invalid recovery key"))
		Expect(memoryStore.closed).To(BeTrue())
	})

	DescribeTable("rejects invalid media ports before constructing a server",
		func(name string) {
			environment[name] = "invalid"

			err := run(context.Background(), testDependencies)

			Expect(err).To(MatchError(ContainSubstring("invalid port")))
			Expect(serverHandler).To(BeNil())
			Expect(memoryStore.closed).To(BeTrue())
		},
		Entry("UDP", "MEDIA_UDP_PORT"),
		Entry("TCP", "MEDIA_TCP_PORT"),
	)

	It("constructs the API and returns a plain HTTP serving failure", func() {
		environment["HTTP_ADDR"] = "127.0.0.1:18080"
		environment["PUBLIC_BASE_URL"] = "https://deck.example"
		server.listenError = errors.New("listen failed")
		bootstrapResult.GeneratedPassword = "one-time-password"

		err := run(context.Background(), testDependencies)

		Expect(err).To(MatchError("serve HTTP: listen failed"))
		Expect(openedDatabaseURL).To(Equal("postgres://memory"))
		Expect(serverAddress).To(Equal("127.0.0.1:18080"))
		Expect(serverHandler).NotTo(BeNil())
		Expect(server.plainCalls).To(Equal(1))
		Expect(server.tlsCalls).To(BeZero())
		Expect(memoryStore.closed).To(BeTrue())
	})

	It("requires the TLS certificate and private key together", func() {
		environment["TLS_CERT_FILE"] = "/cert.pem"

		err := run(context.Background(), testDependencies)

		Expect(err).To(MatchError("serve HTTP: TLS_CERT_FILE and TLS_KEY_FILE must be configured together"))
		Expect(server.plainCalls).To(BeZero())
		Expect(server.tlsCalls).To(BeZero())
	})

	It("starts TLS with configured credentials", func() {
		environment["TLS_CERT_FILE"] = "/cert.pem"
		environment["TLS_KEY_FILE"] = "/key.pem"
		server.listenError = errors.New("tls failed")

		err := run(context.Background(), testDependencies)

		Expect(err).To(MatchError("serve HTTP: tls failed"))
		Expect(server.tlsCalls).To(Equal(1))
		Expect(server.certificate).To(Equal("/cert.pem"))
		Expect(server.privateKey).To(Equal("/key.pem"))
	})

	It("treats a closed server as a successful stop", func() {
		server.listenError = http.ErrServerClosed

		Expect(run(context.Background(), testDependencies)).To(Succeed())
	})

	It("shuts down gracefully when the context is canceled", func() {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- run(ctx, testDependencies) }()
		Eventually(server.started).Should(BeClosed())

		cancel()

		Eventually(result).Should(Receive(BeNil()))
		Expect(server.shutdownCalls).To(Equal(1))
		Expect(memoryStore.closed).To(BeTrue())
	})

	It("returns a graceful shutdown failure", func() {
		ctx, cancel := context.WithCancel(context.Background())
		server.shutdownError = errors.New("shutdown failed")
		result := make(chan error, 1)
		go func() { result <- run(ctx, testDependencies) }()
		Eventually(server.started).Should(BeClosed())

		cancel()

		Eventually(result).Should(Receive(MatchError("shutdown failed")))
	})

	It("starts an APNs worker from the configured secret and stops it with the server", func() {
		environment["APNS_KEY_ID"] = "KEY123"
		environment["APNS_TEAM_ID"] = "TEAM123"
		environment["APNS_TOPIC"] = "com.nowcent.simdeck.voip"
		environment["APNS_PRIVATE_KEY_FILE"] = "/run/secrets/apns_private_key"
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- run(ctx, testDependencies) }()

		Eventually(server.started).Should(BeClosed())
		Eventually(pushWorker.started).Should(BeClosed())
		Expect(privateKeyPath).To(Equal("/run/secrets/apns_private_key"))
		Expect(pushConfig.KeyID).To(Equal("KEY123"))
		Expect(pushConfig.TeamID).To(Equal("TEAM123"))
		Expect(pushConfig.Topic).To(Equal("com.nowcent.simdeck.voip"))
		Expect(pushConfig.PrivateKeyPEM).To(Equal([]byte("private-key")))

		cancel()
		Eventually(result).Should(Receive(BeNil()))
	})

	It("stops HTTP serving when the APNs worker fails", func() {
		environment["APNS_KEY_ID"] = "KEY123"
		environment["APNS_TEAM_ID"] = "TEAM123"
		environment["APNS_TOPIC"] = "com.nowcent.simdeck.voip"
		environment["APNS_PRIVATE_KEY_FILE"] = "/run/secrets/apns_private_key"
		pushWorker.err = errors.New("worker failed")

		Expect(run(context.Background(), testDependencies)).To(MatchError("run push worker: worker failed"))
		Expect(server.shutdownCalls).To(Equal(1))
	})

	DescribeTable("rejects incomplete APNs configuration",
		func(missing string) {
			environment["APNS_KEY_ID"] = "KEY123"
			environment["APNS_TEAM_ID"] = "TEAM123"
			environment["APNS_TOPIC"] = "com.nowcent.simdeck.voip"
			environment["APNS_PRIVATE_KEY_FILE"] = "/run/secrets/apns_private_key"
			delete(environment, missing)

			err := run(context.Background(), testDependencies)

			Expect(err).To(MatchError(missing + " is required when APNs is configured"))
			Expect(server.plainCalls).To(BeZero())
		},
		Entry("key ID", "APNS_KEY_ID"),
		Entry("team ID", "APNS_TEAM_ID"),
		Entry("topic", "APNS_TOPIC"),
		Entry("private key", "APNS_PRIVATE_KEY_FILE"),
	)

	It("reports APNs secret and client construction failures", func() {
		environment["APNS_KEY_ID"] = "KEY123"
		environment["APNS_TEAM_ID"] = "TEAM123"
		environment["APNS_TOPIC"] = "com.nowcent.simdeck.voip"
		environment["APNS_PRIVATE_KEY_FILE"] = "/run/secrets/apns_private_key"
		testDependencies.readFile = func(string) ([]byte, error) { return nil, errors.New("missing secret") }
		Expect(run(context.Background(), testDependencies)).To(MatchError("read APNs private key: missing secret"))

		testDependencies.readFile = func(string) ([]byte, error) { return []byte("key"), nil }
		pushConfigError = errors.New("invalid key")
		Expect(run(context.Background(), testDependencies)).To(MatchError("configure APNs worker: invalid key"))
	})

	It("builds production dependencies and environment defaults", func() {
		production := productionDependencies()
		Expect(production.getenv).NotTo(BeNil())
		Expect(production.readFile).NotTo(BeNil())
		Expect(production.bootstrapOwner).NotTo(BeNil())
		Expect(production.newPushWorker).NotTo(BeNil())

		opened, err := production.openRepository(context.Background(), "://invalid")
		Expect(err).To(MatchError(ContainSubstring("configure postgres")))
		Expect(opened).To(BeNil())

		handler := http.NotFoundHandler()
		created := production.newHTTPServer("127.0.0.1:18080", handler)
		realServer, ok := created.(*http.Server)
		Expect(ok).To(BeTrue())
		Expect(realServer.Addr).To(Equal("127.0.0.1:18080"))
		Expect(realServer.Handler).NotTo(BeNil())
		Expect(realServer.ReadHeaderTimeout).To(Equal(5 * time.Second))
		Expect(realServer.IdleTimeout).To(Equal(60 * time.Second))

		privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		encodedKey, err := x509.MarshalPKCS8PrivateKey(privateKey)
		Expect(err).NotTo(HaveOccurred())
		worker, err := production.newPushWorker(memoryStore, apns.Config{
			KeyID:         "KEY123",
			TeamID:        "TEAM123",
			Topic:         "com.nowcent.simdeck.voip",
			PrivateKeyPEM: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encodedKey}),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(worker).NotTo(BeNil())

		Expect(envOrDefault(func(string) string { return "" }, "NAME", "fallback")).To(Equal("fallback"))
		Expect(envOrDefault(func(string) string { return "value" }, "NAME", "fallback")).To(Equal("value"))
	})
})
