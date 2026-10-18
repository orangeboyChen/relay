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

const testDeviceUUID = "01900000-0000-7000-8000-000000000001"

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

var _ = Describe("Device identity store", func() {
	It("reports cryptographic entropy failures without creating partial state", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
		store.random = errorReader{}

		_, err := store.LoadOrCreate()

		Expect(err).To(MatchError(ContainSubstring("generate device identity key")))
		_, statErr := os.Stat(filepath.Join(directory, stateFile))
		Expect(errors.Is(statErr, os.ErrNotExist)).To(BeTrue())
	})

	It("reports entropy exhaustion before persisting a discovery credential", func() {
		store := NewStore(GinkgoT().TempDir(), testDeviceUUID)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		store.random = errorReader{}

		_, err = store.PrepareDiscovery(state)

		Expect(err).To(MatchError(ContainSubstring("generate pending device credential")))
	})

	It("creates one private identity and reloads it without replacement", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)

		created, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(created.Version).To(Equal(3))
		Expect(created.DeviceUUID).To(Equal(testDeviceUUID))
		Expect(created.IdentityPrivateKey).To(HaveLen(ed25519.PrivateKeySize))
		Expect(created.PublicKey()).To(HaveLen(ed25519.PublicKeySize))
		Expect(created.CommandPrivateKey).To(HaveLen(32))
		Expect(created.CommandPublicKey()).To(HaveLen(32))
		Expect(created.Credential).To(BeEmpty())

		reloaded, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded).To(Equal(created))

		info, err := os.Stat(filepath.Join(directory, stateFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("derives a stable cross-device SIM fingerprint without exposing the ICCID", func() {
		store := NewStore(GinkgoT().TempDir(), testDeviceUUID)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		first, err := state.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())
		second, err := state.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(Equal(second))
		Expect(first).To(HaveLen(64))
		otherState, err := NewStore(GinkgoT().TempDir(), "01900000-0000-7000-8000-000000000002").LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		moved, err := otherState.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())
		Expect(moved).To(Equal(first))
		_, err = state.FingerprintSIM("not-an-iccid")
		Expect(err).To(MatchError(ContainSubstring("invalid length")))
		_, err = state.FingerprintSIM("898600000000000000x")
		Expect(err).To(MatchError(ContainSubstring("only digits")))
	})

	It("atomically persists a SIM fingerprint and server assignment", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
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
		fingerprint, err := state.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())

		updated, err := store.SetSIMIdentity(state, fingerprint, "01900000-0000-7000-8000-000000000004", 4)

		Expect(err).NotTo(HaveOccurred())
		Expect(updated.SIMFingerprint).To(Equal(fingerprint))
		Expect(updated.SIMID).To(Equal("01900000-0000-7000-8000-000000000004"))
		Expect(updated.NumericID).To(Equal(4))
		reloaded, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded).To(Equal(updated))
	})

	It("supports the staged initial SIM fingerprint and assignment workflow", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
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
		fingerprint, err := state.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())

		state, err = store.SetSIMFingerprint(state, fingerprint)
		Expect(err).NotTo(HaveOccurred())
		state, err = store.SetSIMAssignment(state, "01900000-0000-7000-8000-000000000004", 4)

		Expect(err).NotTo(HaveOccurred())
		Expect(state.SIMFingerprint).To(Equal(fingerprint))
		Expect(state.SIMID).To(Equal("01900000-0000-7000-8000-000000000004"))
		Expect(state.NumericID).To(Equal(4))
	})

	It("rejects a partial SIM identity update without changing durable state", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
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
		originalFingerprint, err := state.FingerprintSIM("8986000000000000000")
		Expect(err).NotTo(HaveOccurred())
		state, err = store.SetSIMIdentity(state, originalFingerprint, state.SIMID, state.NumericID)
		Expect(err).NotTo(HaveOccurred())
		newFingerprint, err := state.FingerprintSIM("8986000000000000001")
		Expect(err).NotTo(HaveOccurred())

		_, err = store.SetSIMIdentity(state, newFingerprint, "invalid-sim-id", 4)

		Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		reloaded, reloadErr := store.LoadOrCreate()
		Expect(reloadErr).NotTo(HaveOccurred())
		Expect(reloaded).To(Equal(state))
	})

	It("prevents a bound container from silently changing its physical slot", func() {
		store := NewStore(GinkgoT().TempDir(), testDeviceUUID)
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

		_, err = store.ConfigureSlot(state, "secondary", 2)

		Expect(errors.Is(err, ErrSlotIdentityMismatch)).To(BeTrue())
	})

	It("formats the owner-verification fingerprint from the identity public key", func() {
		state := State{IdentityPrivateKey: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, ed25519.SeedSize))}

		Expect(state.IdentityFingerprint()).To(Equal("C5 B9 40 ED 3F 65"))
		Expect(State{}.IdentityFingerprint()).To(BeEmpty())
	})

	It("rejects a configured UUID that differs from persisted identity", func() {
		directory := GinkgoT().TempDir()
		created, err := NewStore(directory, testDeviceUUID).LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())

		_, err = NewStore(directory, "01900000-0000-7000-8000-000000000002").LoadOrCreate()

		Expect(errors.Is(err, ErrDeviceUUIDMismatch)).To(BeTrue())
		reloaded, reloadErr := NewStore(directory, testDeviceUUID).LoadOrCreate()
		Expect(reloadErr).NotTo(HaveOccurred())
		Expect(reloaded).To(Equal(created))
	})

	It("rejects a malformed configured UUID before creating state", func() {
		directory := filepath.Join(GinkgoT().TempDir(), "state")

		_, err := NewStore(directory, "device-one").LoadOrCreate()

		Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		_, statErr := os.Stat(directory)
		Expect(errors.Is(statErr, os.ErrNotExist)).To(BeTrue())
	})

	DescribeTable("validates canonical device UUIDs",
		func(value string, expected bool) {
			Expect(ValidDeviceUUID(value)).To(Equal(expected))
		},
		Entry("lowercase UUID", testDeviceUUID, true),
		Entry("uppercase UUID", "01900000-0000-7000-8000-00000000000A", false),
		Entry("missing separators", "01900000000070008000000000000001", false),
		Entry("invalid hex", "01900000-0000-7000-8000-00000000000z", false),
	)

	It("atomically persists the server credential and numeric SIM ID", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		prepared, err := store.PrepareDiscovery(state)
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

	It("migrates a version-one identity without replacing its signing key or UUID", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
		created, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		legacy := created
		legacy.Version = 1
		legacy.CommandPrivateKey = nil
		content, err := json.Marshal(legacy)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, stateFile), content, 0o600)).To(Succeed())

		migrated, err := store.LoadOrCreate()

		Expect(err).NotTo(HaveOccurred())
		Expect(migrated.Version).To(Equal(3))
		Expect(migrated.DeviceUUID).To(Equal(created.DeviceUUID))
		Expect(migrated.IdentityPrivateKey).To(Equal(created.IdentityPrivateKey))
		Expect(migrated.CommandPublicKey()).To(HaveLen(32))
	})

	It("refuses to initialize an empty state path", func() {
		_, err := NewStore(" ", testDeviceUUID).LoadOrCreate()
		Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
	})

	It("reports filesystem failures without generating a replacement identity", func() {
		_, err := NewStore("/dev/null/sim-deck", testDeviceUUID).LoadOrCreate()
		Expect(err).To(MatchError(ContainSubstring("create state directory")))
	})

	DescribeTable("rejects corrupt state instead of silently replacing the identity",
		func(mutate func(*State)) {
			directory := GinkgoT().TempDir()
			store := NewStore(directory, testDeviceUUID)
			state, err := store.LoadOrCreate()
			Expect(err).NotTo(HaveOccurred())
			mutate(&state)
			content, err := json.Marshal(state)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(directory, stateFile), content, 0o600)).To(Succeed())

			_, err = store.LoadOrCreate()
			Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		},
		Entry("unsupported version", func(state *State) { state.Version = 4 }),
		Entry("malformed UUID", func(state *State) { state.DeviceUUID = "not-a-uuid" }),
		Entry("malformed private key", func(state *State) { state.IdentityPrivateKey = []byte("short") }),
		Entry("malformed command key", func(state *State) { state.CommandPrivateKey = []byte("short") }),
		Entry("malformed credential", func(state *State) { state.Credential = "short" }),
		Entry("malformed pending credential", func(state *State) { state.PendingCredential = "short" }),
		Entry("credential without numeric ID", func(state *State) {
			state.Credential = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, tokenBytes))
			state.ServerURL = "https://deck.example"
			state.NumericID = 0
		}),
		Entry("numeric ID without credential", func(state *State) { state.NumericID = 1 }),
		Entry("malformed slot during discovery", func(state *State) {
			state.PendingCredential = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, tokenBytes))
			state.SlotKey = " "
		}),
	)

	It("reuses the pending credential across discovery retries", func() {
		store := NewStore(GinkgoT().TempDir(), testDeviceUUID)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())

		first, err := store.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		second, err := store.PrepareDiscovery(first)
		Expect(err).NotTo(HaveOccurred())

		Expect(second.PendingCredential).To(Equal(first.PendingCredential))
	})

	It("leaves an already bound identity unchanged when discovery is prepared again", func() {
		store := NewStore(GinkgoT().TempDir(), testDeviceUUID)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		prepared, err := store.PrepareDiscovery(state)
		Expect(err).NotTo(HaveOccurred())
		bound, err := store.Bind(prepared, "https://deck.example", 8)
		Expect(err).NotTo(HaveOccurred())

		unchanged, err := store.PrepareDiscovery(bound)

		Expect(err).NotTo(HaveOccurred())
		Expect(unchanged).To(Equal(bound))
	})

	It("reports a state directory failure while persisting a pending credential", func() {
		directory := GinkgoT().TempDir()
		goodStore := NewStore(directory, testDeviceUUID)
		state, err := goodStore.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		occupiedPath := filepath.Join(directory, "occupied")
		Expect(os.WriteFile(occupiedPath, []byte("not a directory"), 0o600)).To(Succeed())

		_, err = NewStore(occupiedPath, testDeviceUUID).PrepareDiscovery(state)

		Expect(err).To(MatchError(ContainSubstring("create state directory")))
	})

	It("reports failure to create an atomic temporary state file", func() {
		directory := GinkgoT().TempDir()
		store := NewStore(directory, testDeviceUUID)
		state, err := store.LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Chmod(directory, 0o500)).To(Succeed())
		DeferCleanup(func() { Expect(os.Chmod(directory, 0o700)).To(Succeed()) })

		_, err = store.PrepareDiscovery(state)

		Expect(err).To(MatchError(ContainSubstring("create temporary identity")))
	})

	DescribeTable("rejects malformed state documents",
		func(content string) {
			directory := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(directory, stateFile), []byte(content), 0o600)).To(Succeed())

			_, err := NewStore(directory, testDeviceUUID).LoadOrCreate()

			Expect(errors.Is(err, ErrInvalidState)).To(BeTrue())
		},
		Entry("invalid JSON", "{"),
		Entry("unknown fields", `{"version":1,"unexpected":true}`),
	)

	It("validates a binding before replacing durable state", func() {
		store := NewStore(GinkgoT().TempDir(), testDeviceUUID)
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
		store := NewStore(directory, testDeviceUUID)
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
