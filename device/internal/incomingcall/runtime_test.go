package incomingcall

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/slotpolicy"
)

type fakeRemote struct {
	recipients       []Recipient
	listError        error
	uploadError      error
	uploadFailures   int
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
	if remote.uploadFailures > 0 {
		remote.uploadFailures--
		return errors.New("temporary server failure")
	}
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

type fakeCallRejecter struct {
	count int
	err   error
}

func (rejecter *fakeCallRejecter) Reject(context.Context) error {
	rejecter.count++
	return rejecter.err
}

type fakePolicy struct{ settings slotpolicy.Settings }

func (policy *fakePolicy) Current() slotpolicy.Settings { return policy.settings }

var _ = Describe("Incoming call runtime", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var (
		state     identity.State
		remote    *fakeRemote
		events    *fakeEventRegistrar
		snapshots chan call.Snapshot
		runtime   *Runtime
		calls     *fakeCallRejecter
		policy    *fakePolicy
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
			IdentityPublicKey:     bytes.Repeat([]byte{8}, ed25519.PublicKeySize),
			KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		}}}
		events = &fakeEventRegistrar{}
		snapshots = make(chan call.Snapshot, 4)
		calls = &fakeCallRejecter{}
		policy = &fakePolicy{settings: slotpolicy.Settings{IncomingCallsEnabled: true}}
		runtime, err = NewRuntime(remote, state, snapshots, events, calls, policy)
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
			IdentityPublicKey:     bytes.Repeat([]byte{8}, ed25519.PublicKeySize),
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

	It("rejects an incoming call locally when owner policy disables calls", func() {
		policy.settings.IncomingCallsEnabled = false

		Expect(runtime.process(context.Background(), call.Snapshot{
			State: call.StateRinging, IncomingNumber: "13500000000",
		})).To(Succeed())

		Expect(calls.count).To(Equal(1))
		Expect(remote.notifications).To(BeEmpty())
		Expect(events.callID).To(BeEmpty())
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
		closedRuntime, err := NewRuntime(remote, state, closedSnapshots, events, calls, policy)
		Expect(err).NotTo(HaveOccurred())
		Expect(closedRuntime.Run(context.Background())).To(MatchError("incoming-call snapshot stream closed"))

	})

	It("retries one encrypted incoming call after a temporary server failure", func() {
		remote.uploadFailures = 1
		runtime.retryInterval = 5 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		go func() {
			snapshots <- call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"}
		}()

		Expect(runtime.Run(ctx)).To(MatchError(context.DeadlineExceeded))
		Expect(remote.notifications).To(HaveLen(2))
		Expect(remote.notifications[1].ID).To(Equal(remote.notifications[0].ID))
		Expect(events.callID).NotTo(BeEmpty())
		Expect(runtime.activeCallID).NotTo(BeEmpty())
	})

	It("discards a pending call notification when the call ends", func() {
		remote.uploadError = errors.New("server unavailable")
		runtime.acceptSnapshot(call.Snapshot{State: call.StateRinging, IncomingNumber: "13500000000"})
		Expect(runtime.deliverPending(context.Background())).To(HaveOccurred())

		runtime.acceptSnapshot(call.Snapshot{State: call.StateEnded})

		Expect(runtime.pending).To(BeNil())
		Expect(runtime.deliverPending(context.Background())).To(Succeed())
		Expect(remote.notifications).To(HaveLen(1))
	})

	It("validates construction dependencies", func() {
		_, err := NewRuntime(nil, state, snapshots, events, calls, policy)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, nil, events, calls, policy)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, snapshots, nil, calls, policy)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, snapshots, events, nil, policy)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, snapshots, events, calls, nil)
		Expect(err).To(HaveOccurred())
		state.Credential = ""
		_, err = NewRuntime(remote, state, snapshots, events, calls, policy)
		Expect(err).To(HaveOccurred())
	})
})

func mustAgreementPublicKey() []byte {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	return privateKey.PublicKey().Bytes()
}
