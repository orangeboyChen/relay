package messageevent

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/callevent"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/sms"
)

type fakeRemote struct {
	recipients []incomingcall.Recipient
	envelopes  []callevent.Envelope
	listError  error
	uploadAt   int
	uploads    int
}

func (remote *fakeRemote) ListInstallationRecipients(context.Context, string, string) ([]incomingcall.Recipient, error) {
	return remote.recipients, remote.listError
}

func (remote *fakeRemote) UploadEvent(_ context.Context, _ string, _ string, envelope callevent.Envelope) error {
	remote.uploads++
	if remote.uploadAt > 0 && remote.uploads == remote.uploadAt {
		return errors.New("server unavailable")
	}
	remote.envelopes = append(remote.envelopes, envelope)
	return nil
}

var _ = Describe("Incoming message publisher", func() {
	const (
		deviceID  = "01900000-0000-7000-8000-000000000001"
		messageID = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	)
	var (
		state      identity.State
		remote     *fakeRemote
		publisher  *Publisher
		privateOne *ecdh.PrivateKey
		privateTwo *ecdh.PrivateKey
		message    sms.ReceivedMessage
	)

	BeforeEach(func() {
		commandKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		_, signingKey, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		state = identity.State{
			DeviceUUID: deviceID, NumericID: 2, Credential: "device-secret",
			CommandPrivateKey: commandKey.Bytes(), IdentityPrivateKey: signingKey,
		}
		privateOne, err = ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		privateTwo, err = ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		remote = &fakeRemote{recipients: []incomingcall.Recipient{
			{InstallationID: "01900000-0000-7000-8000-000000000102", IdentityPublicKey: bytes.Repeat([]byte{2}, ed25519.PublicKeySize), KeyAgreementPublicKey: privateTwo.PublicKey().Bytes()},
			{InstallationID: "01900000-0000-7000-8000-000000000101", IdentityPublicKey: bytes.Repeat([]byte{1}, ed25519.PublicKeySize), KeyAgreementPublicKey: privateOne.PublicKey().Bytes()},
		}}
		publisher, err = NewPublisher(remote, state)
		Expect(err).NotTo(HaveOccurred())
		message = sms.ReceivedMessage{
			ID: messageID, Sender: "+8613500000000", Text: "你好 from EC20F", Segments: 2,
			Timestamp: time.Date(2026, 10, 16, 12, 34, 56, 987654321, time.UTC),
		}
	})

	It("independently encrypts the SMS for every approved installation", func() {
		Expect(publisher.Publish(context.Background(), message)).To(Succeed())

		Expect(remote.envelopes).To(HaveLen(2))
		Expect(remote.envelopes[0].ID).NotTo(Equal(remote.envelopes[1].ID))
		first := openEnvelope(state, remote.envelopes[0], privateOne)
		second := openEnvelope(state, remote.envelopes[1], privateTwo)
		firstInstallation := first.InstallationID
		secondInstallation := second.InstallationID
		first.InstallationID = ""
		second.InstallationID = ""
		Expect(first).To(Equal(second))
		Expect(first.MessageID).To(Equal(message.ID))
		Expect(first.DeviceUUID).To(Equal(deviceID))
		Expect(first.NumericID).To(Equal(2))
		Expect(first.RemoteNumber).To(Equal("+8613500000000"))
		Expect(first.Body).To(Equal("你好 from EC20F"))
		Expect(first.Segments).To(Equal(2))
		Expect(firstInstallation).To(Equal("01900000-0000-7000-8000-000000000101"))
		Expect(secondInstallation).To(Equal("01900000-0000-7000-8000-000000000102"))
		for _, envelope := range remote.envelopes {
			Expect(identity.ValidDeviceUUID(envelope.ID)).To(BeTrue())
			Expect(envelope.Type).To(Equal("message.received"))
			Expect(envelope.OccurredAt.Nanosecond()).To(Equal(987000000))
			Expect(ed25519.Verify(state.PublicKey(), signedData(envelope), envelope.Signature)).To(BeTrue())
			Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("13500000000"))
			Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("EC20F"))
		}
	})

	It("cannot be opened by a different installation and detects tampering", func() {
		Expect(publisher.Publish(context.Background(), message)).To(Succeed())
		envelope := remote.envelopes[0]

		_, err := tryOpenEnvelope(state, envelope, privateTwo)
		Expect(err).To(HaveOccurred())
		tampered := envelope
		tampered.Ciphertext = append([]byte(nil), envelope.Ciphertext...)
		tampered.Ciphertext[0] ^= 0xff
		Expect(ed25519.Verify(state.PublicKey(), signedData(tampered), tampered.Signature)).To(BeFalse())
	})

	It("recreates identical envelopes so partial retries remain idempotent", func() {
		remote.uploadAt = 2
		Expect(publisher.Publish(context.Background(), message)).To(MatchError(ContainSubstring("upload message event")))
		Expect(remote.envelopes).To(HaveLen(1))
		firstAttempt := remote.envelopes[0]

		remote.uploadAt = 0
		remote.uploads = 0
		remote.envelopes = nil
		Expect(publisher.Publish(context.Background(), message)).To(Succeed())

		Expect(remote.envelopes).To(HaveLen(2))
		Expect(remote.envelopes[0]).To(Equal(firstAttempt))
	})

	It("keeps identical modem history events isolated by device identity", func() {
		Expect(publisher.Publish(context.Background(), message)).To(Succeed())
		firstDeviceEventID := remote.envelopes[0].ID

		otherDeviceState := state
		otherDeviceState.DeviceUUID = "01900000-0000-7000-8000-000000000002"
		otherDevicePublisher, err := NewPublisher(remote, otherDeviceState)
		Expect(err).NotTo(HaveOccurred())
		remote.envelopes = nil
		Expect(otherDevicePublisher.Publish(context.Background(), message)).To(Succeed())

		Expect(remote.envelopes[0].ID).NotTo(Equal(firstDeviceEventID))
	})

	It("keeps messages pending when recipients or the server are unavailable", func() {
		remote.recipients = nil
		Expect(publisher.Publish(context.Background(), message)).To(MatchError(ErrNoRecipients))

		remote.listError = errors.New("database unavailable")
		Expect(publisher.Publish(context.Background(), message)).To(MatchError(ContainSubstring("list message recipients")))
	})

	It("encrypts detected SIM metadata without exposing the phone number to the server", func() {
		state.SlotID = "01900000-0000-7000-8000-000000000201"
		state.SIMID = "01900000-0000-7000-8000-000000000301"
		publisher.identity = state

		Expect(publisher.PublishSIMProfile(context.Background(), SIMProfile{
			PhoneNumber: "+8613500000000",
			LineName:    "CHINA MOBILE",
		})).To(Succeed())

		Expect(remote.envelopes).To(HaveLen(2))
		envelope := remote.envelopes[0]
		Expect(envelope.Type).To(Equal("sim.profile"))
		Expect(envelope.RecipientInstallationID).To(Equal("01900000-0000-7000-8000-000000000102"))
		Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("13500000000"))
		Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("CHINA MOBILE"))
		Expect(ed25519.Verify(state.PublicKey(), signedData(envelope), envelope.Signature)).To(BeTrue())

		payload := openSIMProfileEnvelope(state, envelope, privateTwo)
		Expect(payload.PhoneNumber).To(Equal("+8613500000000"))
		Expect(payload.LineName).To(Equal("CHINA MOBILE"))
		Expect(payload.SlotID).To(Equal(state.SlotID))
		Expect(payload.SIMID).To(Equal(state.SIMID))
	})

	It("rejects empty or malformed detected SIM metadata", func() {
		Expect(publisher.PublishSIMProfile(context.Background(), SIMProfile{})).To(MatchError("detected SIM profile is invalid"))
		Expect(publisher.PublishSIMProfile(context.Background(), SIMProfile{PhoneNumber: "secret-number"})).To(MatchError("detected SIM profile is invalid"))
		Expect(publisher.PublishSIMProfile(context.Background(), SIMProfile{LineName: strings.Repeat("x", 65)})).To(MatchError("detected SIM profile is invalid"))
	})

	It("accepts an eSIM profile that only exposes its EID", func() {
		profile := SIMProfile{SIMKind: "eSIM", EID: "89000000000000000000000000000001"}

		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(Succeed())
		payload := openSIMProfileEnvelope(publisher.identity, remote.envelopes[0], privateTwo)
		Expect(payload.SIMKind).To(Equal("eSIM"))
		Expect(payload.EID).To(Equal(profile.EID))
	})

	It("encrypts current radio and SIM status for every approved installation", func() {
		state.SlotID = "01900000-0000-7000-8000-000000000201"
		state.SIMID = "01900000-0000-7000-8000-000000000301"
		state.SlotKey = "primary"
		state.SlotIndex = 1
		state.NumericID = 2
		publisher.identity = state
		reportedAt := time.Date(2026, 10, 30, 1, 2, 3, 4000000, time.UTC)

		Expect(publisher.PublishDeviceStatus(context.Background(), DeviceStatus{
			RadioAccessTechnology: "lte",
			SignalBars:            intPointer(3),
			ReportedAt:            reportedAt,
		})).To(Succeed())

		Expect(remote.envelopes).To(HaveLen(2))
		for index, envelope := range remote.envelopes {
			Expect(envelope.Type).To(Equal("device.status"))
			Expect(ed25519.Verify(state.PublicKey(), signedData(envelope), envelope.Signature)).To(BeTrue())
			privateKey := privateOne
			if index == 0 {
				privateKey = privateTwo
			}
			payload := openDeviceStatusEnvelope(state, envelope, privateKey)
			Expect(payload.DeviceUUID).To(Equal(deviceID))
			Expect(payload.SlotID).To(Equal(state.SlotID))
			Expect(payload.SIMID).To(Equal(state.SIMID))
			Expect(payload.Online).To(BeTrue())
			Expect(payload.RadioAccessTechnology).To(Equal("lte"))
			Expect(payload.SignalBars).NotTo(BeNil())
			Expect(*payload.SignalBars).To(Equal(3))
			Expect(payload.ReportedAt).To(Equal(reportedAt.Format(time.RFC3339Nano)))
		}
	})

	It("keeps detected SIM metadata pending when delivery is unavailable", func() {
		profile := SIMProfile{PhoneNumber: "+8613500000000", LineName: "CHINA MOBILE"}

		remote.recipients = nil
		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(MatchError(ErrNoRecipients))

		remote.listError = errors.New("database unavailable")
		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(MatchError(ContainSubstring("list SIM profile recipients")))

		remote.listError = nil
		remote.recipients = []incomingcall.Recipient{{
			InstallationID:        "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{1}, ed25519.PublicKeySize),
			KeyAgreementPublicKey: privateOne.PublicKey().Bytes(),
		}}
		remote.uploadAt = 1
		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(MatchError(ContainSubstring("upload SIM profile event")))
	})

	It("rejects unusable SIM profile recipient and key material", func() {
		profile := SIMProfile{LineName: "CHINA MOBILE"}
		remote.recipients[0].KeyAgreementPublicKey = []byte("short")
		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(MatchError("SIM profile recipient identity is invalid"))

		remote.recipients[0] = incomingcall.Recipient{
			InstallationID:        "01900000-0000-7000-8000-000000000102",
			IdentityPublicKey:     bytes.Repeat([]byte{2}, ed25519.PublicKeySize),
			KeyAgreementPublicKey: make([]byte, 32),
		}
		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(MatchError(ContainSubstring("derive recipient SIM profile key")))

		publisher.identity.CommandPrivateKey = []byte("short")
		remote.recipients[0].KeyAgreementPublicKey = privateTwo.PublicKey().Bytes()
		Expect(publisher.PublishSIMProfile(context.Background(), profile)).To(MatchError(ContainSubstring("parse device SIM profile key")))
	})

	It("fails closed when a random SIM profile event ID cannot be generated", func() {
		_, err := randomEventID(strings.NewReader("short"))
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("rejects unsafe message and recipient data",
		func(mutate func(*sms.ReceivedMessage, *[]incomingcall.Recipient)) {
			candidate := message
			recipients := append([]incomingcall.Recipient(nil), remote.recipients...)
			mutate(&candidate, &recipients)
			remote.recipients = recipients
			Expect(publisher.Publish(context.Background(), candidate)).To(HaveOccurred())
		},
		Entry("invalid message ID", func(value *sms.ReceivedMessage, _ *[]incomingcall.Recipient) { value.ID = "invalid" }),
		Entry("invalid sender", func(value *sms.ReceivedMessage, _ *[]incomingcall.Recipient) { value.Sender = "not a number" }),
		Entry("empty body", func(value *sms.ReceivedMessage, _ *[]incomingcall.Recipient) { value.Text = " " }),
		Entry("invalid segments", func(value *sms.ReceivedMessage, _ *[]incomingcall.Recipient) { value.Segments = 0 }),
		Entry("invalid recipient", func(_ *sms.ReceivedMessage, value *[]incomingcall.Recipient) {
			(*value)[0].KeyAgreementPublicKey = []byte("short")
		}),
		Entry("duplicate recipient", func(_ *sms.ReceivedMessage, value *[]incomingcall.Recipient) { (*value)[1] = (*value)[0] }),
	)

	It("validates publisher dependencies", func() {
		_, err := NewPublisher(nil, state)
		Expect(err).To(HaveOccurred())
		_, err = NewPublisher(remote, identity.State{})
		Expect(err).To(HaveOccurred())
	})

	It("rejects unusable local and recipient key material", func() {
		publisher.identity.CommandPrivateKey = []byte("short")
		Expect(publisher.Publish(context.Background(), message)).To(MatchError(ContainSubstring("parse device message key")))

		publisher.identity = state
		remote.recipients[0].KeyAgreementPublicKey = make([]byte, 32)
		Expect(publisher.Publish(context.Background(), message)).To(MatchError(ContainSubstring("derive recipient message key")))
	})
})

func openEnvelope(state identity.State, envelope callevent.Envelope, privateKey *ecdh.PrivateKey) Payload {
	payload, err := tryOpenEnvelope(state, envelope, privateKey)
	Expect(err).NotTo(HaveOccurred())
	return payload
}

func openSIMProfileEnvelope(state identity.State, envelope callevent.Envelope, privateKey *ecdh.PrivateKey) SIMProfilePayload {
	devicePublic, err := ecdh.X25519().NewPublicKey(state.CommandPublicKey())
	Expect(err).NotTo(HaveOccurred())
	sharedSecret, err := privateKey.ECDH(devicePublic)
	Expect(err).NotTo(HaveOccurred())
	aead, err := eventAEADWithContext(sharedSecret, envelope.ID, envelope.DeviceUUID, simProfileContext)
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	Expect(err).NotTo(HaveOccurred())
	var payload SIMProfilePayload
	Expect(json.Unmarshal(plaintext, &payload)).To(Succeed())
	return payload
}

func openDeviceStatusEnvelope(state identity.State, envelope callevent.Envelope, privateKey *ecdh.PrivateKey) DeviceStatusPayload {
	devicePublic, err := ecdh.X25519().NewPublicKey(state.CommandPublicKey())
	Expect(err).NotTo(HaveOccurred())
	sharedSecret, err := privateKey.ECDH(devicePublic)
	Expect(err).NotTo(HaveOccurred())
	aead, err := eventAEADWithContext(sharedSecret, envelope.ID, envelope.DeviceUUID, deviceStatusContext)
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	Expect(err).NotTo(HaveOccurred())
	var payload DeviceStatusPayload
	Expect(json.Unmarshal(plaintext, &payload)).To(Succeed())
	return payload
}

func intPointer(value int) *int { return &value }

func tryOpenEnvelope(state identity.State, envelope callevent.Envelope, privateKey *ecdh.PrivateKey) (Payload, error) {
	devicePublic, err := ecdh.X25519().NewPublicKey(state.CommandPublicKey())
	if err != nil {
		return Payload{}, err
	}
	sharedSecret, err := privateKey.ECDH(devicePublic)
	if err != nil {
		return Payload{}, err
	}
	aead, err := eventAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID)
	if err != nil {
		return Payload{}, err
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	if err != nil {
		return Payload{}, err
	}
	var payload Payload
	if err := json.Unmarshal(plaintext, &payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}
