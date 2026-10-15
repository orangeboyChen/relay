package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/serverapi"
)

type fakePairingClient struct {
	credentials []string
	result      serverapi.PairingResult
	err         error
}

func (client *fakePairingClient) Pair(_ context.Context, _ string, credential string, _ identity.State) (serverapi.PairingResult, error) {
	client.credentials = append(client.credentials, credential)
	return client.result, client.err
}

var _ = Describe("Device server binding", func() {
	var (
		directory string
		store     identity.Store
		state     identity.State
		config    Config
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		store = identity.NewStore(filepath.Join(directory, "state"))
		var err error
		state, err = store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		secretPath := filepath.Join(directory, "pairing-token")
		Expect(os.WriteFile(secretPath, []byte("one-time-token\n"), 0o600)).To(Succeed())
		config = Config{ServerURL: "https://deck.example", PairingTokenFile: secretPath}
	})

	It("persists the same pending credential across a failed request and retry", func() {
		failedClient := &fakePairingClient{err: errors.New("connection interrupted")}
		_, err := EnsureBinding(context.Background(), config, store, state, failedClient)
		Expect(err).To(MatchError("connection interrupted"))
		Expect(failedClient.credentials).To(HaveLen(1))

		pending, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(pending.PendingCredential).To(Equal(failedClient.credentials[0]))

		successClient := &fakePairingClient{result: serverapi.PairingResult{NumericID: 4}}
		bound, err := EnsureBinding(context.Background(), config, store, pending, successClient)
		Expect(err).NotTo(HaveOccurred())
		Expect(successClient.credentials).To(Equal(failedClient.credentials))
		Expect(bound.Credential).To(Equal(failedClient.credentials[0]))
		Expect(bound.PendingCredential).To(BeEmpty())
		Expect(bound.NumericID).To(Equal(4))
	})

	It("does not require the pairing secret after binding", func() {
		prepared, err := store.PreparePairing(state)
		Expect(err).NotTo(HaveOccurred())
		bound, err := store.Bind(prepared, config.ServerURL, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(config.PairingTokenFile)).To(Succeed())
		client := &fakePairingClient{err: errors.New("must not be called")}

		result, err := EnsureBinding(context.Background(), config, store, bound, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(bound))
		Expect(client.credentials).To(BeEmpty())
	})

	It("refuses to send an existing credential to a different server", func() {
		prepared, err := store.PreparePairing(state)
		Expect(err).NotTo(HaveOccurred())
		bound, err := store.Bind(prepared, config.ServerURL, 2)
		Expect(err).NotTo(HaveOccurred())
		config.ServerURL = "https://other.example"

		_, err = EnsureBinding(context.Background(), config, store, bound, &fakePairingClient{})

		Expect(err).To(MatchError("configured server does not match the server bound to this device identity"))
	})

	It("keeps the pending credential when the pairing token secret is missing", func() {
		Expect(os.Remove(config.PairingTokenFile)).To(Succeed())
		client := &fakePairingClient{}

		_, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).To(MatchError(ContainSubstring("open pairing token secret")))
		Expect(client.credentials).To(BeEmpty())
		persisted, loadErr := store.LoadOrCreate()
		Expect(loadErr).NotTo(HaveOccurred())
		Expect(persisted.PendingCredential).NotTo(BeEmpty())
	})

	It("reports a durable prepare failure before contacting the server", func() {
		badStore := identity.NewStore(filepath.Join(directory, "not-a-directory"))
		Expect(os.WriteFile(filepath.Join(directory, "not-a-directory"), []byte("occupied"), 0o600)).To(Succeed())
		client := &fakePairingClient{}

		_, err := EnsureBinding(context.Background(), config, badStore, state, client)

		Expect(err).To(MatchError(ContainSubstring("prepare device pairing")))
		Expect(client.credentials).To(BeEmpty())
	})

	It("rejects an invalid SIM numeric ID returned by the server", func() {
		client := &fakePairingClient{result: serverapi.PairingResult{NumericID: 100}}

		_, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).To(MatchError(ContainSubstring("persist device binding")))
		persisted, loadErr := store.LoadOrCreate()
		Expect(loadErr).NotTo(HaveOccurred())
		Expect(persisted.PendingCredential).To(Equal(client.credentials[0]))
		Expect(persisted.Credential).To(BeEmpty())
	})
})
