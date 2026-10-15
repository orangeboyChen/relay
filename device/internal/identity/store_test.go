package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

var _ = Describe("Device identity store", func() {
	It("reports cryptographic entropy failures without creating partial state", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory)
		store.random = errorReader{}

		_, err := store.LoadOrCreate()

		Expect(err).To(MatchError(ContainSubstring("generate device identity key")))
		_, statErr := os.Stat(filepath.Join(directory, stateFile))
		Expect(errors.Is(statErr, os.ErrNotExist)).To(BeTrue())
	})

	It("reports UUID entropy exhaustion after generating the identity key", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory)
		store.random = bytes.NewReader(make([]byte, ed25519.SeedSize))

		_, err := store.LoadOrCreate()

		Expect(err).To(MatchError(ContainSubstring("generate device UUID")))
		_, statErr := os.Stat(filepath.Join(directory, stateFile))
		Expect(errors.Is(statErr, os.ErrNotExist)).To(BeTrue())
	})

	It("reports entropy exhaustion before persisting a pairing credential", func() {
		store := NewStore(GinkgoT().TempDir())
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		store.random = errorReader{}

		_, err = store.PreparePairing(state)

		Expect(err).To(MatchError(ContainSubstring("generate pending device credential")))
	})

	It("creates one private identity and reloads it without replacement", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory)

		created, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Version).To(Equal(1))
		Expect(created.DeviceUUID).To(HaveLen(36))
		Expect(created.IdentityPrivateKey).To(HaveLen(ed25519.PrivateKeySize))
		Expect(created.PublicKey()).To(HaveLen(ed25519.PublicKeySize))
		Expect(created.Credential).To(BeEmpty())

		reloaded, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded).To(Equal(created))

		info, err := os.Stat(filepath.Join(directory, stateFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("atomically persists the server credential and numeric SIM ID", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		prepared, err := store.PreparePairing(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(prepared.PendingCredential).NotTo(BeEmpty())

		bound, err := store.Bind(prepared, "https://deck.example/", 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(bound.Credential).To(Equal(prepared.PendingCredential))
		Expect(bound.PendingCredential).To(BeEmpty())
		Expect(bound.NumericID).To(Equal(7))
		Expect(bound.ServerURL).To(Equal("https://deck.example"))

		reloaded, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded).To(Equal(bound))
	})

	It("refuses to initialize an empty state path", func() {
		_, err := NewStore(" ").LoadOrCreate()
		Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
	})

	It("reports filesystem failures without generating a replacement identity", func() {
		_, err := NewStore("/dev/null/sim-deck").LoadOrCreate()
		Expect(err).To(MatchError(ContainSubstring("create state directory")))
	})

	DescribeTable("rejects corrupt state instead of silently replacing the identity",
		func(mutate func(*State)) {
			directory := GinkgoT().TempDir()
			store := NewStore(directory)
			state, err := store.LoadOrCreate()
			Expect(err).NotTo(HaveOccurred())
			mutate(&state)
			content, err := json.Marshal(state)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(directory, stateFile), content, 0o600)).To(Succeed())

			_, err = store.LoadOrCreate()
			Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		},
		Entry("unsupported version", func(state *State) { state.Version = 2 }),
		Entry("malformed UUID", func(state *State) { state.DeviceUUID = "not-a-uuid" }),
		Entry("malformed private key", func(state *State) { state.IdentityPrivateKey = []byte("short") }),
		Entry("malformed credential", func(state *State) { state.Credential = "short" }),
		Entry("malformed pending credential", func(state *State) { state.PendingCredential = "short" }),
		Entry("credential without numeric ID", func(state *State) {
			state.Credential = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, tokenBytes))
			state.ServerURL = "https://deck.example"
			state.NumericID = 0
		}),
		Entry("numeric ID without credential", func(state *State) { state.NumericID = 1 }),
	)

	It("reuses the pending credential across pairing retries", func() {
		store := NewStore(GinkgoT().TempDir())
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())

		first, err := store.PreparePairing(state)
		Expect(err).NotTo(HaveOccurred())
		second, err := store.PreparePairing(first)
		Expect(err).NotTo(HaveOccurred())

		Expect(second.PendingCredential).To(Equal(first.PendingCredential))
	})

	It("leaves an already bound identity unchanged when pairing is prepared again", func() {
		store := NewStore(GinkgoT().TempDir())
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		prepared, err := store.PreparePairing(state)
		Expect(err).NotTo(HaveOccurred())
		bound, err := store.Bind(prepared, "https://deck.example", 8)
		Expect(err).NotTo(HaveOccurred())

		unchanged, err := store.PreparePairing(bound)

		Expect(err).NotTo(HaveOccurred())
		Expect(unchanged).To(Equal(bound))
	})

	It("reports a state directory failure while persisting a pending credential", func() {
		directory := GinkgoT().TempDir()
		goodStore := NewStore(directory)
		state, err := goodStore.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		occupiedPath := filepath.Join(directory, "occupied")
		Expect(os.WriteFile(occupiedPath, []byte("not a directory"), 0o600)).To(Succeed())

		_, err = NewStore(occupiedPath).PreparePairing(state)

		Expect(err).To(MatchError(ContainSubstring("create state directory")))
	})

	It("reports failure to create an atomic temporary state file", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chmod(directory, 0o500)).To(Succeed())
		DeferCleanup(func() { Expect(os.Chmod(directory, 0o700)).To(Succeed()) })

		_, err = store.PreparePairing(state)

		Expect(err).To(MatchError(ContainSubstring("create temporary identity")))
	})

	DescribeTable("rejects malformed state documents",
		func(content string) {
			directory := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(directory, stateFile), []byte(content), 0o600)).To(Succeed())

			_, err := NewStore(directory).LoadOrCreate()

			Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		},
		Entry("invalid JSON", "{"),
		Entry("unknown fields", `{"version":1,"unexpected":true}`),
	)

	It("validates a binding before replacing durable state", func() {
		store := NewStore(GinkgoT().TempDir())
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())

		state.PendingCredential = "malformed"
		_, err = store.Bind(state, "https://deck.example", 1)

		Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		reloaded, reloadErr := store.LoadOrCreate()
		Expect(reloadErr).NotTo(HaveOccurred())
		Expect(reloaded.Credential).To(BeEmpty())
	})

	It("surfaces an atomic replacement failure and leaves no credential behind", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		path := filepath.Join(directory, stateFile)
		Expect(os.Remove(path)).To(Succeed())
		Expect(os.Mkdir(path, 0o700)).To(Succeed())
		state.PendingCredential = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, tokenBytes))

		_, err = store.Bind(state, "https://deck.example", 1)

		Expect(err).To(MatchError(ContainSubstring("replace identity")))
	})

	It("returns no public key for an invalid in-memory private key", func() {
		Expect((State{IdentityPrivateKey: []byte("short")}).PublicKey()).To(BeNil())
	})
})
