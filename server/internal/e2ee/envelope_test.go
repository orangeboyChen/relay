package e2ee_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/e2ee"
)

var _ = Describe("Event envelopes", func() {
	var (
		publicKey  ed25519.PublicKey
		privateKey ed25519.PrivateKey
		vaultKey   []byte
		metadata   domain.EventEnvelope
	)

	BeforeEach(func() {
		var err error
		publicKey, privateKey, err = ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())

		vaultKey = make([]byte, 32)
		_, err = rand.Read(vaultKey)
		Expect(err).NotTo(HaveOccurred())

		metadata = e2ee.NewEnvelope(
			"event-1",
			0,
			"message.received",
			"device-1",
			time.Unix(1_700_000_000, 123).UTC(),
			2,
		)
	})

	Describe("Seal and Open", func() {
		It("round trips encrypted content", func() {
			sealed, err := e2ee.Seal(vaultKey, privateKey, metadata, []byte("private message"))
			Expect(err).NotTo(HaveOccurred())
			Expect(sealed.Nonce).To(HaveLen(12))
			Expect(sealed.Signature).To(HaveLen(ed25519.SignatureSize))

			plaintext, err := e2ee.Open(vaultKey, publicKey, sealed)
			Expect(err).NotTo(HaveOccurred())
			Expect(plaintext).To(Equal([]byte("private message")))
		})

		It("keeps the server-assigned sequence outside the signature and AEAD data", func() {
			sealed, err := e2ee.Seal(vaultKey, privateKey, metadata, []byte("private message"))
			Expect(err).NotTo(HaveOccurred())

			sealed.Sequence = 42
			Expect(e2ee.VerifyEnvelope(publicKey, sealed)).To(Succeed())
			plaintext, err := e2ee.Open(vaultKey, publicKey, sealed)
			Expect(err).NotTo(HaveOccurred())
			Expect(plaintext).To(Equal([]byte("private message")))
		})

		It("rejects the wrong vault key after accepting a valid signature", func() {
			sealed, err := e2ee.Seal(vaultKey, privateKey, metadata, []byte("private message"))
			Expect(err).NotTo(HaveOccurred())

			wrongKey := append([]byte(nil), vaultKey...)
			wrongKey[0] ^= 0xff
			_, err = e2ee.Open(wrongKey, publicKey, sealed)
			Expect(err).To(MatchError(ContainSubstring("decrypt event envelope")))
		})
	})

	Describe("VerifyEnvelope", func() {
		var sealed domain.EventEnvelope

		BeforeEach(func() {
			var err error
			sealed, err = e2ee.Seal(vaultKey, privateKey, metadata, []byte("private message"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("authenticates a valid envelope without decrypting it", func() {
			Expect(e2ee.VerifyEnvelope(publicKey, sealed)).To(Succeed())
		})

		DescribeTable("rejects tampering with signed data",
			func(mutate func(*domain.EventEnvelope)) {
				mutate(&sealed)
				Expect(e2ee.VerifyEnvelope(publicKey, sealed)).To(MatchError(e2ee.ErrInvalidSignature))
			},
			Entry("event ID", func(envelope *domain.EventEnvelope) { envelope.ID = "event-2" }),
			Entry("event type", func(envelope *domain.EventEnvelope) { envelope.Type = "call.started" }),
			Entry("device UUID", func(envelope *domain.EventEnvelope) { envelope.DeviceUUID = "device-2" }),
			Entry("timestamp", func(envelope *domain.EventEnvelope) { envelope.OccurredAt = envelope.OccurredAt.Add(time.Second) }),
			Entry("key epoch", func(envelope *domain.EventEnvelope) { envelope.KeyEpoch++ }),
			Entry("nonce", func(envelope *domain.EventEnvelope) { envelope.Nonce[0] ^= 0xff }),
			Entry("ciphertext", func(envelope *domain.EventEnvelope) { envelope.Ciphertext[0] ^= 0xff }),
		)

		It("rejects a different public key", func() {
			otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
			Expect(err).NotTo(HaveOccurred())
			Expect(e2ee.VerifyEnvelope(otherPublicKey, sealed)).To(MatchError(e2ee.ErrInvalidSignature))
		})

		It("rejects a malformed verification key", func() {
			Expect(e2ee.VerifyEnvelope(ed25519.PublicKey("short"), sealed)).To(MatchError(e2ee.ErrInvalidSigningKey))
		})

		DescribeTable("rejects malformed envelopes before signature verification",
			func(mutate func(*domain.EventEnvelope)) {
				mutate(&sealed)
				err := e2ee.VerifyEnvelope(publicKey, sealed)
				Expect(errors.Is(err, e2ee.ErrInvalidEnvelope)).To(BeTrue())
			},
			Entry("missing ID", func(envelope *domain.EventEnvelope) { envelope.ID = " " }),
			Entry("oversized ID", func(envelope *domain.EventEnvelope) { envelope.ID = strings.Repeat("a", e2ee.MaxEventIDBytes+1) }),
			Entry("negative sequence", func(envelope *domain.EventEnvelope) { envelope.Sequence = -1 }),
			Entry("missing type", func(envelope *domain.EventEnvelope) { envelope.Type = "" }),
			Entry("oversized type", func(envelope *domain.EventEnvelope) { envelope.Type = strings.Repeat("a", e2ee.MaxEventTypeBytes+1) }),
			Entry("missing device UUID", func(envelope *domain.EventEnvelope) { envelope.DeviceUUID = "" }),
			Entry("oversized device UUID", func(envelope *domain.EventEnvelope) {
				envelope.DeviceUUID = strings.Repeat("a", e2ee.MaxDeviceUUIDBytes+1)
			}),
			Entry("missing timestamp", func(envelope *domain.EventEnvelope) { envelope.OccurredAt = time.Time{} }),
			Entry("invalid key epoch", func(envelope *domain.EventEnvelope) { envelope.KeyEpoch = 0 }),
			Entry("short nonce", func(envelope *domain.EventEnvelope) { envelope.Nonce = make([]byte, 11) }),
			Entry("short ciphertext", func(envelope *domain.EventEnvelope) { envelope.Ciphertext = make([]byte, 15) }),
			Entry("oversized ciphertext", func(envelope *domain.EventEnvelope) { envelope.Ciphertext = make([]byte, e2ee.MaxCiphertextBytes+1) }),
			Entry("short signature", func(envelope *domain.EventEnvelope) { envelope.Signature = make([]byte, ed25519.SignatureSize-1) }),
		)
	})

	Describe("input validation", func() {
		It("rejects invalid vault keys", func() {
			_, err := e2ee.Seal([]byte("short"), privateKey, metadata, nil)
			Expect(err).To(MatchError(e2ee.ErrInvalidVaultKey))

			_, err = e2ee.Open([]byte("short"), publicKey, domain.EventEnvelope{})
			Expect(err).To(MatchError(e2ee.ErrInvalidVaultKey))
		})

		It("rejects an invalid private key", func() {
			_, err := e2ee.Seal(vaultKey, ed25519.PrivateKey("short"), metadata, nil)
			Expect(err).To(MatchError(e2ee.ErrInvalidSigningKey))
		})

		It("rejects invalid routing metadata before encryption", func() {
			metadata.Type = ""
			_, err := e2ee.Seal(vaultKey, privateKey, metadata, nil)
			Expect(errors.Is(err, e2ee.ErrInvalidEnvelope)).To(BeTrue())
		})

		It("rejects plaintext that cannot fit in a bounded envelope", func() {
			plaintext := make([]byte, e2ee.MaxCiphertextBytes)
			_, err := e2ee.Seal(vaultKey, privateKey, metadata, plaintext)
			Expect(errors.Is(err, e2ee.ErrInvalidEnvelope)).To(BeTrue())
		})
	})
})
