package incomingcall

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
)

type fakeRemote struct {
	recipients       []Recipient
	listError        error
	uploadError      error
	listedDeviceID   string
	listedCredential string
	uploadedDeviceID string
	uploadCredential string
	notifications    []Notification
}

func (remote *fakeRemote) ListInstallationRecipients(_ context.Context, deviceID string, credential string) ([]Recipient, error) {
	remote.listedDeviceID = deviceID
	remote.listedCredential = credential
	return remote.recipients, remote.listError
}

func (remote *fakeRemote) UploadIncomingCall(_ context.Context, deviceID string, credential string, notification Notification) error {
	remote.uploadedDeviceID = deviceID
	remote.uploadCredential = credential
	remote.notifications = append(remote.notifications, notification)
	return remote.uploadError
}

type fakeEventRegistrar struct {
	callID        string
	sharedSecret  []byte
	registerError error
	unregistered  []string
}

func (registrar *fakeEventRegistrar) RegisterIncoming(callID string, sharedSecret []byte) error {
	registrar.callID = callID
	registrar.sharedSecret = append([]byte(nil), sharedSecret...)
	return registrar.registerError
}

func (registrar *fakeEventRegistrar) Unregister(callID string) {
	registrar.unregistered = append(registrar.unregistered, callID)
}

var _ = Describe("Incoming call runtime", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var (
		state     identity.State
		remote    *fakeRemote
		events    *fakeEventRegistrar
		snapshots chan call.Snapshot
		runtime   *Runtime
	)

	BeforeEach(func() {
		_, signingKey, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		agreementKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		state = identity.State{
			DeviceUUID: deviceID, NumericID: 2, IdentityPrivateKey: signingKey, Credential: "device-secret",
		}
		remote = &fakeRemote{recipients: []Recipient{{
			InstallationID:        "01900000-0000-7000-8000-000000000101",
			KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		}}}
		events = &fakeEventRegistrar{}
		snapshots = make(chan call.Snapshot, 4)
		runtime, err = NewRuntime(remote, state, snapshots, events)
		Expect(err).NotTo(HaveOccurred())
		runtime.builder.random = &sequenceReader{next: 1}
	})

	It("encrypts and uploads one notification before registering call state", func() {
		err := runtime.process(context.Background(), call.Snapshot{
			State: call.StateRinging, IncomingNumber: "+8613500000000",
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(remote.listedDeviceID).To(Equal(deviceID))
		Expect(remote.listedCredential).To(Equal("device-secret"))
		Expect(remote.uploadedDeviceID).To(Equal(deviceID))
		Expect(remote.uploadCredential).To(Equal("device-secret"))
		Expect(remote.notifications).To(HaveLen(1))
		Expect(events.callID).NotTo(BeEmpty())
		Expect(events.sharedSecret).To(HaveLen(32))
		Expect(runtime.activeCallID).To(Equal(events.callID))
	})

	It("suppresses incomplete, unrelated, duplicate, and recipientless transitions", func() {
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging})).To(Succeed())
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateActive, IncomingNumber: "13500000000"})).To(Succeed())
		Expect(remote.notifications).To(BeEmpty())

		remote.recipients = nil
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(Succeed())
		Expect(remote.notifications).To(BeEmpty())

		remote.recipients = []Recipient{{
			InstallationID:        "01900000-0000-7000-8000-000000000101",
			KeyAgreementPublicKey: mustAgreementPublicKey(),
		}}
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(Succeed())
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateWaiting, IncomingNumber: "13500000000"})).To(Succeed())
		Expect(remote.notifications).To(HaveLen(1))
	})

	It("clears the active context when the cellular call ends", func() {
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(Succeed())
		callID := runtime.activeCallID

		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateEnded})).To(Succeed())

		Expect(events.unregistered).To(Equal([]string{callID}))
		Expect(runtime.activeCallID).To(BeEmpty())
	})

	It("surfaces recipient, upload, encryption, and registration failures", func() {
		remote.listError = errors.New("server unavailable")
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(MatchError(ContainSubstring("list incoming call recipients")))

		remote.listError = nil
		remote.uploadError = errors.New("server unavailable")
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(MatchError(ContainSubstring("upload incoming call")))

		remote.uploadError = nil
		remote.recipients[0].KeyAgreementPublicKey = []byte("short")
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(MatchError(ContainSubstring("encrypt incoming call")))

		remote.recipients[0].KeyAgreementPublicKey = mustAgreementPublicKey()
		events.registerError = errors.New("registration failed")
		Expect(runtime.process(context.Background(), call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})).To(MatchError(ContainSubstring("register incoming call events")))
	})

	It("runs until cancellation, stream closure, or a processing failure", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(runtime.Run(ctx)).To(MatchError(context.Canceled))

		closedSnapshots := make(chan call.Snapshot)
		close(closedSnapshots)
		closedRuntime, err := NewRuntime(remote, state, closedSnapshots, events)
		Expect(err).NotTo(HaveOccurred())
		Expect(closedRuntime.Run(context.Background())).To(MatchError("incoming-call snapshot stream closed"))

		remote.listError = errors.New("server unavailable")
		snapshots <- call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"}
		Expect(runtime.Run(context.Background())).To(MatchError(ContainSubstring("server unavailable")))
	})

	It("validates construction dependencies", func() {
		_, err := NewRuntime(nil, state, snapshots, events)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, nil, events)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, snapshots, nil)
		Expect(err).To(HaveOccurred())
		state.Credential = ""
		_, err = NewRuntime(remote, state, snapshots, events)
		Expect(err).To(HaveOccurred())
	})
})

func mustAgreementPublicKey() []byte {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	return privateKey.PublicKey().Bytes()
}
