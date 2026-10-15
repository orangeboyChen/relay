package command

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
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
	claims        []*Envelope
	claimError    error
	completeError error
	deviceID      string
	credential    string
	commandID     string
	result        EncryptedResult
}

func (remote *fakeRemote) Claim(context.Context, string, string) (*Envelope, error) {
	if remote.claimError != nil {
		return nil, remote.claimError
	}
	if len(remote.claims) == 0 {
		return nil, nil
	}
	claimed := remote.claims[0]
	remote.claims = remote.claims[1:]
	return claimed, nil
}

func (remote *fakeRemote) Complete(_ context.Context, deviceID string, credential string, commandID string, result EncryptedResult) error {
	remote.deviceID = deviceID
	remote.credential = credential
	remote.commandID = commandID
	remote.result = result
	return remote.completeError
}

type fakeCalls struct {
	state      call.State
	operations []string
	error      error
}

func (calls *fakeCalls) Dial(_ context.Context, value string) error {
	calls.operations = append(calls.operations, "dial:"+value)
	if calls.error == nil {
		calls.state = call.StateDialing
	}
	return calls.error
}
func (calls *fakeCalls) Hangup(context.Context) error {
	calls.operations = append(calls.operations, "hangup")
	return calls.error
}
func (calls *fakeCalls) Hold(context.Context) error {
	calls.operations = append(calls.operations, "hold")
	return calls.error
}
func (calls *fakeCalls) Resume(context.Context) error {
	calls.operations = append(calls.operations, "resume")
	return calls.error
}
func (calls *fakeCalls) Swap(context.Context) error {
	calls.operations = append(calls.operations, "swap")
	return calls.error
}
func (calls *fakeCalls) SendDTMF(_ context.Context, value string) error {
	calls.operations = append(calls.operations, "dtmf:"+value)
	return calls.error
}
func (calls *fakeCalls) State() call.State { return calls.state }

var _ = Describe("Encrypted command runtime", func() {
	const (
		deviceID  = "01900000-0000-7000-8000-000000000001"
		commandID = "01900000-0000-7000-8000-000000000010"
	)
	var (
		state     identity.State
		senderKey *ecdh.PrivateKey
		remote    *fakeRemote
		calls     *fakeCalls
		runtime   *Runtime
	)

	BeforeEach(func() {
		deviceKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		senderKey, err = ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		state = identity.State{
			Version: 2, DeviceUUID: deviceID, CommandPrivateKey: deviceKey.Bytes(), Credential: "device-secret",
		}
		remote = &fakeRemote{}
		calls = &fakeCalls{state: call.StateIdle}
		runtime, err = NewRuntime(remote, state, calls, time.Millisecond)
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("executes supported call operations from authenticated plaintext",
		func(payload Payload, expectedOperation string) {
			envelope, sharedSecret := sealCommand(state, senderKey, commandID, payload)

			Expect(runtime.process(context.Background(), envelope)).To(Succeed())

			Expect(calls.operations).To(Equal([]string{expectedOperation}))
			Expect(remote.deviceID).To(Equal(deviceID))
			Expect(remote.credential).To(Equal("device-secret"))
			Expect(remote.commandID).To(Equal(commandID))
			result := openResult(sharedSecret, envelope, remote.result)
			Expect(result.Success).To(BeTrue())
		},
		Entry("start", Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "+8613500000000"}, "dial:+8613500000000"),
		Entry("end", Payload{Version: 1, Operation: "call.end", CallID: "call-1"}, "hangup"),
		Entry("hold", Payload{Version: 1, Operation: "call.hold", CallID: "call-1"}, "hold"),
		Entry("resume", Payload{Version: 1, Operation: "call.resume", CallID: "call-1"}, "resume"),
		Entry("swap", Payload{Version: 1, Operation: "call.swap", CallID: "call-1"}, "swap"),
		Entry("DTMF", Payload{Version: 1, Operation: "call.dtmf", CallID: "call-1", Digits: "#"}, "dtmf:#"),
	)

	It("returns an encrypted generic modem error", func() {
		calls.error = errors.New("ATD exposed detail")
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086"})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		result := openResult(sharedSecret, envelope, remote.result)
		Expect(result.Success).To(BeFalse())
		Expect(result.ErrorCode).To(Equal("modem_rejected"))
		Expect(string(remote.result.Ciphertext)).NotTo(ContainSubstring("ATD exposed detail"))
	})

	It("rejects unsupported or malformed plaintext without invoking the modem", func() {
		for _, payload := range []Payload{
			{Version: 2, Operation: "call.end", CallID: "call-1"},
			{Version: 1, Operation: "call.end"},
			{Version: 1, Operation: "unknown", CallID: "call-1"},
		} {
			envelope, sharedSecret := sealCommand(state, senderKey, commandID, payload)
			Expect(runtime.process(context.Background(), envelope)).To(Succeed())
			Expect(openResult(sharedSecret, envelope, remote.result).ErrorCode).To(Equal("invalid_command"))
		}
		Expect(calls.operations).To(BeEmpty())
	})

	It("rejects tampering before invoking the modem or acknowledging", func() {
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.end", CallID: "call-1"})
		envelope.Ciphertext[0] ^= 0xff

		err := runtime.process(context.Background(), envelope)

		Expect(err).To(MatchError(ContainSubstring("decrypt command")))
		Expect(calls.operations).To(BeEmpty())
		Expect(remote.commandID).To(BeEmpty())
	})

	It("surfaces completion and entropy failures", func() {
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.end", CallID: "call-1"})
		remote.completeError = errors.New("server unavailable")
		Expect(runtime.process(context.Background(), envelope)).To(MatchError(ContainSubstring("complete command")))

		remote.completeError = nil
		runtime.random = errorReader{}
		Expect(runtime.process(context.Background(), envelope)).To(MatchError(ContainSubstring("encrypt command result")))
	})

	It("polls until canceled and surfaces claim failures", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(runtime.Run(ctx)).To(MatchError(context.Canceled))

		remote.claimError = errors.New("server unavailable")
		Expect(runtime.Run(context.Background())).To(MatchError("server unavailable"))
	})

	It("validates runtime dependencies", func() {
		_, err := NewRuntime(nil, state, calls, time.Second)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, identity.State{}, calls, time.Second)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, nil, time.Second)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, calls, 0)
		Expect(err).To(HaveOccurred())
	})
})

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func sealCommand(state identity.State, senderKey *ecdh.PrivateKey, commandID string, payload Payload) (Envelope, []byte) {
	devicePublic, err := ecdh.X25519().NewPublicKey(state.CommandPublicKey())
	Expect(err).NotTo(HaveOccurred())
	sharedSecret, err := senderKey.ECDH(devicePublic)
	Expect(err).NotTo(HaveOccurred())
	aead, err := testAEAD(sharedSecret, commandID, state.DeviceUUID, "sim-deck-command-v1")
	Expect(err).NotTo(HaveOccurred())
	nonce := make([]byte, nonceBytes)
	_, err = rand.Read(nonce)
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return Envelope{
		ID: commandID, DeviceUUID: state.DeviceUUID, SenderPublicKey: senderKey.PublicKey().Bytes(), Nonce: nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, associatedData("sim-deck-command-v1", commandID, state.DeviceUUID)),
	}, sharedSecret
}

func openResult(sharedSecret []byte, envelope Envelope, encrypted EncryptedResult) Result {
	aead, err := testAEAD(sharedSecret, envelope.ID, envelope.DeviceUUID, "sim-deck-command-result-v1")
	Expect(err).NotTo(HaveOccurred())
	plaintext, err := aead.Open(nil, encrypted.Nonce, encrypted.Ciphertext, associatedData("sim-deck-command-result-v1", envelope.ID, envelope.DeviceUUID))
	Expect(err).NotTo(HaveOccurred())
	var result Result
	Expect(json.Unmarshal(plaintext, &result)).To(Succeed())
	return result
}

func testAEAD(sharedSecret []byte, commandID string, deviceID string, context string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, sharedSecret, []byte(commandID+"\x00"+deviceID), context, keyBytes)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
