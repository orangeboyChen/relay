package e2ee

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Incoming call notification verification", func() {
	var (
		publicKey    ed25519.PublicKey
		privateKey   ed25519.PrivateKey
		notification domain.IncomingCallNotification
	)

	BeforeEach(func() {
		var err error
		publicKey, privateKey, err = ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		notification = domain.IncomingCallNotification{
			ID:         "01900000-0000-7000-8000-000000000010",
			Type:       "call.incoming",
			DeviceUUID: "01900000-0000-7000-8000-000000000001",
			OccurredAt: time.Date(2026, 10, 16, 8, 9, 10, 123000000, time.UTC),
			KeyEpoch:   1,
			Recipients: []domain.IncomingCallRecipient{
				{
					InstallationID:  "01900000-0000-7000-8000-000000000101",
					SenderPublicKey: bytes.Repeat([]byte{1}, 32),
					Nonce:           bytes.Repeat([]byte{2}, 12),
					Ciphertext:      bytes.Repeat([]byte{3}, 32),
				},
				{
					InstallationID:  "01900000-0000-7000-8000-000000000102",
					SenderPublicKey: bytes.Repeat([]byte{4}, 32),
					Nonce:           bytes.Repeat([]byte{5}, 12),
					Ciphertext:      bytes.Repeat([]byte{6}, 32),
				},
			},
		}
		for index := range notification.Recipients {
			recipient := &notification.Recipients[index]
			recipient.Signature = ed25519.Sign(privateKey, incomingCallRecipientSignedData(notification, *recipient))
		}
	})

	It("accepts canonical recipient ciphertexts signed by the device", func() {
		Expect(VerifyIncomingCallNotification(publicKey, notification)).To(Succeed())
	})

	DescribeTable("detects tampering with every signed field",
		func(mutate func(*domain.IncomingCallNotification)) {
			mutate(&notification)
			Expect(VerifyIncomingCallNotification(publicKey, notification)).To(MatchError(ErrInvalidSignature))
		},
		Entry("notification ID", func(value *domain.IncomingCallNotification) { value.ID = "01900000-0000-7000-8000-000000000011" }),
		Entry("device ID", func(value *domain.IncomingCallNotification) {
			value.DeviceUUID = "01900000-0000-7000-8000-000000000002"
		}),
		Entry("timestamp", func(value *domain.IncomingCallNotification) { value.OccurredAt = value.OccurredAt.Add(time.Second) }),
		Entry("key epoch", func(value *domain.IncomingCallNotification) { value.KeyEpoch++ }),
		Entry("installation ID", func(value *domain.IncomingCallNotification) {
			value.Recipients[0].InstallationID = "01900000-0000-7000-8000-000000000100"
		}),
		Entry("sender key", func(value *domain.IncomingCallNotification) { value.Recipients[0].SenderPublicKey[0] ^= 0xff }),
		Entry("nonce", func(value *domain.IncomingCallNotification) { value.Recipients[0].Nonce[0] ^= 0xff }),
		Entry("ciphertext", func(value *domain.IncomingCallNotification) { value.Recipients[0].Ciphertext[0] ^= 0xff }),
	)

	DescribeTable("rejects malformed routing and recipient metadata",
		func(mutate func(*domain.IncomingCallNotification)) {
			mutate(&notification)
			err := VerifyIncomingCallNotification(publicKey, notification)
			Expect(errors.Is(err, ErrInvalidIncomingCall)).To(BeTrue())
		},
		Entry("invalid notification ID", func(value *domain.IncomingCallNotification) { value.ID = "invalid" }),
		Entry("invalid type", func(value *domain.IncomingCallNotification) { value.Type = "call.ringing" }),
		Entry("invalid device ID", func(value *domain.IncomingCallNotification) { value.DeviceUUID = "invalid" }),
		Entry("missing timestamp", func(value *domain.IncomingCallNotification) { value.OccurredAt = time.Time{} }),
		Entry("invalid key epoch", func(value *domain.IncomingCallNotification) { value.KeyEpoch = 0 }),
		Entry("no recipients", func(value *domain.IncomingCallNotification) { value.Recipients = nil }),
		Entry("too many recipients", func(value *domain.IncomingCallNotification) {
			value.Recipients = make([]domain.IncomingCallRecipient, maxIncomingCallRecipients+1)
		}),
		Entry("invalid installation ID", func(value *domain.IncomingCallNotification) { value.Recipients[0].InstallationID = "invalid" }),
		Entry("short sender key", func(value *domain.IncomingCallNotification) { value.Recipients[0].SenderPublicKey = []byte("short") }),
		Entry("short nonce", func(value *domain.IncomingCallNotification) { value.Recipients[0].Nonce = make([]byte, 11) }),
		Entry("short ciphertext", func(value *domain.IncomingCallNotification) { value.Recipients[0].Ciphertext = make([]byte, 15) }),
		Entry("oversized ciphertext", func(value *domain.IncomingCallNotification) {
			value.Recipients[0].Ciphertext = make([]byte, maxIncomingCiphertext+1)
		}),
		Entry("duplicate recipient", func(value *domain.IncomingCallNotification) {
			value.Recipients[1].InstallationID = value.Recipients[0].InstallationID
		}),
		Entry("unsorted recipients", func(value *domain.IncomingCallNotification) {
			value.Recipients[0], value.Recipients[1] = value.Recipients[1], value.Recipients[0]
		}),
		Entry("short signature", func(value *domain.IncomingCallNotification) { value.Recipients[0].Signature = []byte("short") }),
	)

	It("distinguishes malformed signing keys from invalid signatures", func() {
		Expect(VerifyIncomingCallNotification(ed25519.PublicKey("short"), notification)).To(MatchError(ErrInvalidSigningKey))
		otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		Expect(VerifyIncomingCallNotification(otherPublicKey, notification)).To(MatchError(ErrInvalidSignature))
	})
})
