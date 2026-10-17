package callevent

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
		Expect(payload).To(Equal(Payload{Version: 1, CallID: "call-secret-id", DeviceUUID: state.DeviceUUID, State: call.StateActive, Active: true}))
	})

	It("registers incoming calls with the same encrypted state channel", func() {
		Expect(publisher.RegisterIncoming("incoming-call", sharedSecret)).To(Succeed())

		Expect(publisher.publish(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(Succeed())

		Expect(remote.envelopes).To(HaveLen(1))
		var payload Payload
		Expect(json.Unmarshal(openEvent(sharedSecret, remote.envelopes[0]), &payload)).To(Succeed())
		Expect(payload.CallID).To(Equal("incoming-call"))
		Expect(payload.State).To(Equal(call.StateRinging))
	})

	It("matches the iOS call event compatibility vector", func() {
		seed := make([]byte, ed25519.SeedSize)
		for index := range seed {
			seed[index] = byte(index + 65)
		}
		devicePrivate, err := ecdh.X25519().NewPrivateKey(sequence(1, 32))
		Expect(err).NotTo(HaveOccurred())
		senderPrivate, err := ecdh.X25519().NewPrivateKey(sequence(33, 32))
		Expect(err).NotTo(HaveOccurred())
		secret, err := senderPrivate.ECDH(devicePrivate.PublicKey())
		Expect(err).NotTo(HaveOccurred())
		envelope, err := seal(
			zeroReader{},
			secret,
			ed25519.NewKeyFromSeed(seed),
			Envelope{
				ID: "01900000-0000-7000-8000-000000000011", Type: eventType, DeviceUUID: deviceID,
				OccurredAt: time.Date(2026, 10, 16, 8, 9, 10, 0, time.UTC), KeyEpoch: 1,
			},
			[]byte(`{"version":1,"call_id":"018f0000-0000-7000-8000-000000000001","state":"active","active":true,"held":false,"waiting":false}`),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(base64.StdEncoding.EncodeToString(envelope.Ciphertext)).To(Equal("r7ZfXWX5iFdtSt7NFmMawU9ANd92JPGaOC9VbXwQ1JfXcNVloVdhs82ohy9chIWeACMGiKQxY6Z6qXHp7ZHqdDAlOIyuOkd4odiqTyBWk2kp/1Xo09yHgiqYroKoTc2ngTF1QJKjhj877JI8joks7dhKZ4hyp4O8NZa4r9YAZ5YbuAv9EOJLnjyO"))
		Expect(base64.StdEncoding.EncodeToString(envelope.Signature)).To(Equal("WxQ9bm1NtcF3JSTvIXwIY27QcxJpqcYeLy+x5Ra9+3m1feoBsBOIIdYwmBlEJsuCV92qKFRlN3V2eiyofIHNCQ=="))
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

	It("stops when the call snapshot source closes", func() {
		close(snapshots)

		Expect(publisher.Run(context.Background())).To(MatchError("call event snapshot stream closed"))
		Expect(remote.envelopes).To(BeEmpty())
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

func sequence(start byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = start + byte(index)
	}
	return result
}

func openEvent(sharedSecret []byte, envelope Envelope) []byte {
	aead, err := eventAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID)
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, associatedData(envelope))
	Expect(err).NotTo(HaveOccurred())
	return plaintext
}
