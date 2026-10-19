package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/serverapi"
)

type fakeDiscoveryClient struct {
	submittedCredentials []string
	statusCredentials    []string
	submitResult         serverapi.DiscoveryResult
	submitError          error
	submitResults        []serverapi.DiscoveryResult
	submitErrors         []error
	statusResults        []serverapi.DiscoveryResult
	statusErrors         []error
}

func (client *fakeDiscoveryClient) SubmitDiscovery(_ context.Context, credential string, _ identity.State) (serverapi.DiscoveryResult, error) {
	client.submittedCredentials = append(client.submittedCredentials, credential)
	index := len(client.submittedCredentials) - 1
	if index < len(client.submitErrors) && client.submitErrors[index] != nil {
		return serverapi.DiscoveryResult{}, client.submitErrors[index]
	}
	if index < len(client.submitResults) {
		return client.submitResults[index], nil
	}
	return client.submitResult, client.submitError
}

func (client *fakeDiscoveryClient) GetDiscoveryStatus(_ context.Context, _ string, credential string) (serverapi.DiscoveryResult, error) {
	client.statusCredentials = append(client.statusCredentials, credential)
	index := len(client.statusCredentials) - 1
	if index < len(client.statusErrors) && client.statusErrors[index] != nil {
		return serverapi.DiscoveryResult{}, client.statusErrors[index]
	}
	if index < len(client.statusResults) {
		return client.statusResults[index], nil
	}
	return serverapi.DiscoveryResult{}, serverapi.ErrDiscoveryUnavailable
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
		store = identity.NewStore(filepath.Join(directory, "state"), "01900000-0000-7000-8000-000000000001")
		var err error
		state, err = store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		config = Config{DeviceUUID: state.DeviceUUID, ServerURL: "https://deck.example", DiscoveryPoll: time.Millisecond}
	})

	It("submits once and persists approval after polling", func() {
		client := &fakeDiscoveryClient{
			statusErrors: []error{serverapi.ErrDiscoveryUnavailable, nil},
			statusResults: []serverapi.DiscoveryResult{
				{}, {DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryApproved, NumericID: 4},
			},
			submitResult: serverapi.DiscoveryResult{DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryPending},
		}

		bound, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(client.submittedCredentials).To(HaveLen(1))
		Expect(client.statusCredentials).To(HaveLen(2))
		Expect(client.statusCredentials[0]).To(Equal(client.submittedCredentials[0]))
		Expect(bound.Credential).To(Equal(client.submittedCredentials[0]))
		Expect(bound.PendingCredential).To(BeEmpty())
		Expect(bound.NumericID).To(Equal(4))
	})

	It("recovers an approval after restart without submitting again", func() {
		prepared, err := store.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		client := &fakeDiscoveryClient{statusResults: []serverapi.DiscoveryResult{{
			DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryApproved, NumericID: 2,
		}}}

		bound, err := EnsureBinding(context.Background(), config, store, prepared, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(client.submittedCredentials).To(BeEmpty())
		Expect(client.statusCredentials).To(Equal([]string{prepared.PendingCredential}))
		Expect(bound.NumericID).To(Equal(2))
	})

	It("does not contact discovery after the identity is bound", func() {
		prepared, err := store.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		bound, err := store.Bind(prepared, config.ServerURL, 2)
		Expect(err).NotTo(HaveOccurred())
		client := &fakeDiscoveryClient{submitError: errors.New("must not be called")}

		result, err := EnsureBinding(context.Background(), config, store, bound, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(bound))
		Expect(client.submittedCredentials).To(BeEmpty())
		Expect(client.statusCredentials).To(BeEmpty())
	})

	It("refuses to send an existing credential to a different server", func() {
		prepared, err := store.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		bound, err := store.Bind(prepared, config.ServerURL, 2)
		Expect(err).NotTo(HaveOccurred())
		config.ServerURL = "https://other.example"

		_, err = EnsureBinding(context.Background(), config, store, bound, &fakeDiscoveryClient{})

		Expect(err).To(MatchError("configured server does not match the server bound to this device identity"))
	})

	It("waits for the owner to open discovery without losing the durable credential", func() {
		client := &fakeDiscoveryClient{
			statusErrors: []error{
				serverapi.ErrDiscoveryUnavailable,
				serverapi.ErrDiscoveryUnavailable,
				nil,
			},
			statusResults: []serverapi.DiscoveryResult{
				{},
				{},
				{DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryApproved, NumericID: 3},
			},
			submitErrors: []error{serverapi.ErrDiscoveryUnavailable, nil},
			submitResults: []serverapi.DiscoveryResult{
				{},
				{DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryPending},
			},
		}

		bound, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(client.submittedCredentials).To(HaveLen(2))
		Expect(bound.NumericID).To(Equal(3))
		persisted, loadErr := store.LoadOrCreate()
		Expect(loadErr).NotTo(HaveOccurred())
		Expect(persisted.Credential).To(Equal(client.submittedCredentials[0]))
	})

	It("keeps the durable credential when submission fails permanently", func() {
		client := &fakeDiscoveryClient{
			statusErrors: []error{serverapi.ErrDiscoveryUnavailable}, submitError: errors.New("server unavailable"),
		}

		_, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).To(MatchError("server unavailable"))
		persisted, loadErr := store.LoadOrCreate()
		Expect(loadErr).NotTo(HaveOccurred())
		Expect(persisted.PendingCredential).To(Equal(client.submittedCredentials[0]))
	})

	It("reports a durable prepare failure before contacting the server", func() {
		badStore := identity.NewStore(filepath.Join(directory, "not-a-directory"), state.DeviceUUID)
		Expect(os.WriteFile(filepath.Join(directory, "not-a-directory"), []byte("occupied"), 0o600)).To(Succeed())
		client := &fakeDiscoveryClient{}

		_, err := EnsureBinding(context.Background(), config, badStore, state, client)

		Expect(err).To(MatchError(ContainSubstring("prepare device discovery")))
		Expect(client.submittedCredentials).To(BeEmpty())
		Expect(client.statusCredentials).To(BeEmpty())
	})

	It("stops when the owner rejects the candidate", func() {
		client := &fakeDiscoveryClient{statusResults: []serverapi.DiscoveryResult{{
			DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryRejected,
		}}}

		_, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).To(MatchError("device discovery was rejected by the owner"))
	})

	It("resubmits when a pending approval expires", func() {
		client := &fakeDiscoveryClient{
			statusResults: []serverapi.DiscoveryResult{
				{DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryPending},
				{},
				{DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryApproved, NumericID: 5},
			},
			statusErrors: []error{nil, serverapi.ErrDiscoveryUnavailable, nil},
			submitResult: serverapi.DiscoveryResult{
				DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryPending,
			},
		}

		bound, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).NotTo(HaveOccurred())
		Expect(client.submittedCredentials).To(HaveLen(1))
		Expect(bound.NumericID).To(Equal(5))
	})

	It("honors cancellation while waiting for approval", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client := &fakeDiscoveryClient{statusResults: []serverapi.DiscoveryResult{{Status: serverapi.DiscoveryPending}}}

		_, err := EnsureBinding(ctx, config, store, state, client)

		Expect(err).To(MatchError(context.Canceled))
	})

	DescribeTable("rejects invalid server discovery results",
		func(result serverapi.DiscoveryResult, expected string) {
			client := &fakeDiscoveryClient{statusResults: []serverapi.DiscoveryResult{result}}
			_, err := EnsureBinding(context.Background(), config, store, state, client)
			Expect(err).To(MatchError(ContainSubstring(expected)))
		},
		Entry("empty", serverapi.DiscoveryResult{}, "empty device discovery"),
		Entry("unknown", serverapi.DiscoveryResult{Status: "unknown"}, "unsupported device discovery"),
	)

	It("requires a positive discovery polling interval", func() {
		config.DiscoveryPoll = 0

		_, err := EnsureBinding(context.Background(), config, store, state, &fakeDiscoveryClient{})

		Expect(err).To(MatchError("device discovery poll interval must be positive"))
	})

	It("rejects an invalid approved SIM numeric ID before replacing state", func() {
		client := &fakeDiscoveryClient{statusResults: []serverapi.DiscoveryResult{{
			DeviceUUID: state.DeviceUUID, Status: serverapi.DiscoveryApproved, NumericID: 100,
		}}}

		_, err := EnsureBinding(context.Background(), config, store, state, client)

		Expect(err).To(MatchError(ContainSubstring("persist device binding")))
		persisted, loadErr := store.LoadOrCreate()
		Expect(loadErr).NotTo(HaveOccurred())
		Expect(persisted.PendingCredential).NotTo(BeEmpty())
		Expect(persisted.Credential).To(BeEmpty())
	})
})
