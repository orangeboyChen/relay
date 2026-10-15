package callevent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
)

type fakeRemote struct {
	deviceID   string
	credential string
	envelopes  []Envelope
	err        error
}

func (remote *fakeRemote) UploadEvent(_ context.Context, deviceID string, credential string, envelope Envelope) error {
	remote.deviceID = deviceID
	remote.credential = credential
	remote.envelopes = append(remote.envelopes, envelope)
	return remote.err
}

var _ = Describe("Encrypted call event publisher", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var (
		privateKey   ed25519.PrivateKey
		state        identity.State
		remote       *fakeRemote
		snapshots    chan call.Snapshot
		publisher    *Publisher
		sharedSecret []byte
	)

	BeforeEach(func() {
		_, privateKey, _ = ed25519.GenerateKey(rand.Reader)
		state = identity.State{DeviceUUID: deviceID, IdentityPrivateKey: privateKey, Credential: "device-secret"}
		remote = &fakeRemote{}
		snapshots = make(chan call.Snapshot, 4)
		sharedSecret = make([]byte, keyBytes)
		for index := range sharedSecret {
			sharedSecret[index] = byte(index + 1)
		}
		var err error
		publisher, err = NewPublisher(remote, state, snapshots)
		Expect(err).NotTo(HaveOccurred())
		publisher.now = func() time.Time { return time.Date(2026, 10, 16, 8, 9, 10, 123456789, time.UTC) }
		publisher.random = zeroReader{}
	})

	It("encrypts and signs a registered modem transition without exposing call metadata", func() {
		Expect(publisher.RegisterOutgoing("call-secret-id", sharedSecret)).To(Succeed())

		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateActive, Active: true})).To(Succeed())

		Expect(remote.deviceID).To(Equal(deviceID))
		Expect(remote.credential).To(Equal("device-secret"))
		Expect(remote.envelopes).To(HaveLen(1))
		envelope := remote.envelopes[0]
		Expect(envelope.Type).To(Equal("call.state"))
		Expect(envelope.Sequence).To(BeZero())
		Expect(envelope.KeyEpoch).To(Equal(1))
		Expect(envelope.OccurredAt.Nanosecond()).To(Equal(123000000))
		Expect(envelope.Nonce).To(HaveLen(nonceBytes))
		Expect(ed25519.Verify(state.PublicKey(), signedData(envelope), envelope.Signature)).To(BeTrue())
		Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("call-secret-id"))

		plaintext := openEvent(sharedSecret, envelope)
		var payload Payload
		Expect(json.Unmarshal(plaintext, &payload)).To(Succeed())
		Expect(payload).To(Equal(Payload{Version: 1, CallID: "call-secret-id", State: call.StateActive, Active: true}))
	})

	It("ignores transitions without a registered outgoing call", func() {
		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateRinging})).To(Succeed())
		Expect(remote.envelopes).To(BeEmpty())
	})

	It("clears registration only after an ended event uploads successfully", func() {
		Expect(publisher.RegisterOutgoing("call-1", sharedSecret)).To(Succeed())
		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateEnded})).To(Succeed())
		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateActive})).To(Succeed())
		Expect(remote.envelopes).To(HaveLen(1))

		Expect(publisher.RegisterOutgoing("call-2", sharedSecret)).To(Succeed())
		remote.err = errors.New("server unavailable")
		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateEnded})).To(MatchError("upload call event: server unavailable"))
		remote.err = nil
		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateEnded})).To(Succeed())
		Expect(remote.envelopes).To(HaveLen(3))
	})

	It("runs until cancellation and surfaces delivery failures", func() {
		Expect(publisher.RegisterOutgoing("call-1", sharedSecret)).To(Succeed())
		remote.err = errors.New("server unavailable")
		snapshots <- call.Snapshot{State: call.StateDialing}
		Expect(publisher.Run(context.Background())).To(MatchError("upload call event: server unavailable"))

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(publisher.Run(ctx)).To(MatchError(context.Canceled))
	})

	It("validates dependencies and registrations", func() {
		_, err := NewPublisher(nil, state, snapshots)
		Expect(err).To(HaveOccurred())
		_, err = NewPublisher(remote, identity.State{}, snapshots)
		Expect(err).To(HaveOccurred())
		_, err = NewPublisher(remote, state, nil)
		Expect(err).To(HaveOccurred())
		Expect(publisher.RegisterOutgoing("", sharedSecret)).To(HaveOccurred())
		Expect(publisher.RegisterOutgoing("call", []byte("short"))).To(HaveOccurred())
		publisher.Unregister("another-call")
		Expect(publisher.registeredCall()).To(BeNil())
	})

	It("surfaces entropy failures", func() {
		Expect(publisher.RegisterOutgoing("call-1", sharedSecret)).To(Succeed())
		publisher.random = errorReader{}
		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateDialing})).To(MatchError(ContainSubstring("generate call event id")))
	})
})

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = 0
	}
	return len(value), nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func openEvent(sharedSecret []byte, envelope Envelope) []byte {
	aead, err := eventAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID)
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	Expect(err).NotTo(HaveOccurred())
	return plaintext
}
