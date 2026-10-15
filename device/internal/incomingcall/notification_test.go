package incomingcall

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
)

var _ = Describe("Incoming call notification", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var (
		state      identity.State
		builder    *Builder
		privateOne *ecdh.PrivateKey
		privateTwo *ecdh.PrivateKey
		recipients []Recipient
	)

	BeforeEach(func() {
		_, signingKey, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		state = identity.State{DeviceUUID: deviceID, NumericID: 2, IdentityPrivateKey: signingKey}
		builder, err = NewBuilder(state)
		Expect(err).NotTo(HaveOccurred())
		builder.now = func() time.Time { return time.Date(2026, 10, 16, 8, 9, 10, 123456789, time.UTC) }
		builder.random = &sequenceReader{next: 1}
		privateOne, err = ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		privateTwo, err = ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		recipients = []Recipient{
			{InstallationID: "01900000-0000-7000-8000-000000000102", KeyAgreementPublicKey: privateTwo.PublicKey().Bytes()},
			{InstallationID: "01900000-0000-7000-8000-000000000101", KeyAgreementPublicKey: privateOne.PublicKey().Bytes()},
		}
	})

	It("independently encrypts the same call for every approved iPhone", func() {
		notification, callContext, err := builder.Seal(recipients, call.Snapshot{
			State: call.StateRinging, IncomingNumber: "+8613500000000",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(notification.Type).To(Equal("call.incoming"))
		Expect(notification.DeviceUUID).To(Equal(deviceID))
		Expect(notification.OccurredAt.Nanosecond()).To(Equal(123000000))
		Expect(notification.Recipients).To(HaveLen(2))
		Expect(notification.Recipients[0].InstallationID).To(Equal(recipients[1].InstallationID))
		Expect(notification.Recipients[1].InstallationID).To(Equal(recipients[0].InstallationID))
		Expect(ed25519.Verify(state.PublicKey(), signedData(notification), notification.Signature)).To(BeTrue())

		payloadOne := openRecipient(notification, notification.Recipients[0], privateOne)
		payloadTwo := openRecipient(notification, notification.Recipients[1], privateTwo)
		Expect(payloadOne).To(Equal(payloadTwo))
		Expect(payloadOne.Version).To(Equal(1))
		Expect(payloadOne.CallID).To(Equal(callContext.CallID))
		Expect(payloadOne.DeviceUUID).To(Equal(deviceID))
		Expect(payloadOne.NumericID).To(Equal(2))
		Expect(payloadOne.RemoteNumber).To(Equal("+8613500000000"))
		Expect(payloadOne.State).To(Equal(call.StateRinging))
		Expect(payloadOne.CallSecret).To(Equal(callContext.SharedSecret))
		Expect(payloadOne.NotificationID).To(Equal(notification.ID))
		Expect(string(notification.Recipients[0].Ciphertext)).NotTo(ContainSubstring("13500000000"))
		Expect(string(notification.Recipients[1].Ciphertext)).NotTo(ContainSubstring("13500000000"))
	})

	It("binds each ciphertext to its intended installation", func() {
		notification, _, err := builder.Seal(recipients, call.Snapshot{
			State: call.StateWaiting, IncomingNumber: "13500000000",
		})
		Expect(err).NotTo(HaveOccurred())

		sharedSecret, err := privateTwo.ECDH(mustPublicKey(notification.Recipients[0].SenderPublicKey))
		Expect(err).NotTo(HaveOccurred())
		aead, err := recipientAEAD(sharedSecret, notification.ID, notification.Recipients[0].InstallationID)
		Expect(err).NotTo(HaveOccurred())
		_, err = aead.Open(nil, notification.Recipients[0].Nonce, notification.Recipients[0].Ciphertext, recipientAssociatedData(notification, notification.Recipients[0]))
		Expect(err).To(HaveOccurred())

		tampered := notification
		tampered.Recipients = append([]RecipientEnvelope(nil), notification.Recipients...)
		tampered.Recipients[0].InstallationID = recipients[0].InstallationID
		Expect(ed25519.Verify(state.PublicKey(), signedData(tampered), tampered.Signature)).To(BeFalse())
	})

	DescribeTable("rejects unsafe inputs",
		func(mutate func(*[]Recipient, *call.Snapshot)) {
			candidateRecipients := append([]Recipient(nil), recipients...)
			snapshot := call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"}
			mutate(&candidateRecipients, &snapshot)
			_, _, err := builder.Seal(candidateRecipients, snapshot)
			Expect(err).To(HaveOccurred())
		},
		Entry("no recipients", func(value *[]Recipient, _ *call.Snapshot) { *value = nil }),
		Entry("invalid installation ID", func(value *[]Recipient, _ *call.Snapshot) { (*value)[0].InstallationID = "invalid" }),
		Entry("short public key", func(value *[]Recipient, _ *call.Snapshot) { (*value)[0].KeyAgreementPublicKey = []byte("short") }),
		Entry("duplicate recipient", func(value *[]Recipient, _ *call.Snapshot) { (*value)[1] = (*value)[0] }),
		Entry("non-ringing state", func(_ *[]Recipient, value *call.Snapshot) { value.State = call.StateActive }),
		Entry("invalid number", func(_ *[]Recipient, value *call.Snapshot) { value.IncomingNumber = "not a phone number" }),
	)

	It("validates the bound device identity", func() {
		_, err := NewBuilder(identity.State{})
		Expect(err).To(MatchError("incoming-call builder requires a valid bound device identity"))
		state.NumericID = 100
		_, err = NewBuilder(state)
		Expect(err).To(HaveOccurred())
	})

	It("reports entropy failures without returning partial ciphertext", func() {
		builder.random = errorReader{}
		_, _, err := builder.Seal(recipients, call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})
		Expect(err).To(MatchError(ContainSubstring("generate incoming call id")))

		builder.random = &limitedReader{remaining: 32}
		_, _, err = builder.Seal(recipients, call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})
		Expect(err).To(MatchError(ContainSubstring("generate incoming call secret")))

		builder.random = &limitedReader{remaining: 64}
		_, _, err = builder.Seal(recipients, call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})
		Expect(err).To(MatchError(ContainSubstring("generate recipient")))
	})
})

func openRecipient(notification Notification, envelope RecipientEnvelope, privateKey *ecdh.PrivateKey) Payload {
	sharedSecret, err := privateKey.ECDH(mustPublicKey(envelope.SenderPublicKey))
	Expect(err).NotTo(HaveOccurred())
	aead, err := recipientAEAD(sharedSecret, notification.ID, envelope.InstallationID)
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, recipientAssociatedData(notification, envelope))
	Expect(err).NotTo(HaveOccurred())
	var payload Payload
	Expect(json.Unmarshal(plaintext, &payload)).To(Succeed())
	return payload
}

func mustPublicKey(value []byte) *ecdh.PublicKey {
	publicKey, err := ecdh.X25519().NewPublicKey(value)
	Expect(err).NotTo(HaveOccurred())
	return publicKey
}

type sequenceReader struct {
	next byte
}

func (reader *sequenceReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = reader.next
		reader.next++
	}
	return len(value), nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type limitedReader struct {
	remaining int
}

func (reader *limitedReader) Read(value []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	count := len(value)
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := 0; index < count; index++ {
		value[index] = byte(index + 1)
	}
	reader.remaining -= count
	if count < len(value) {
		return count, io.ErrUnexpectedEOF
	}
	return count, nil
}
