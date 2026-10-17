package command

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/call"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
)

const testInstallationID = "01900000-0000-7000-8000-000000000101"

type fakeRemote struct {
	claims         []*Envelope
	claimError     error
	completeError  error
	deviceID       string
	credential     string
	commandID      string
	result         EncryptedResult
	completed      chan struct{}
	recipients     []incomingcall.Recipient
	recipientError error
}

func (remote *fakeRemote) ListInstallationRecipients(context.Context, string, string) ([]incomingcall.Recipient, error) {
	if remote.recipientError != nil {
		return nil, remote.recipientError
	}
	return remote.recipients, nil
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
	if remote.completed != nil {
		close(remote.completed)
	}
	return remote.completeError
}

type fakeCalls struct {
	state      call.State
	operations []string
	error      error
	sequence   *[]string
}

func (calls *fakeCalls) Dial(_ context.Context, value string) error {
	calls.operations = append(calls.operations, "dial:"+value)
	if calls.sequence != nil {
		*calls.sequence = append(*calls.sequence, "dial")
	}
	if calls.error == nil {
		calls.state = call.StateDialing
	}
	return calls.error
}
func (calls *fakeCalls) Answer(context.Context) error {
	calls.operations = append(calls.operations, "answer")
	if calls.error == nil {
		calls.state = call.StateActive
	}
	return calls.error
}
func (calls *fakeCalls) AnswerWaiting(context.Context) error {
	calls.operations = append(calls.operations, "answer-waiting")
	if calls.error == nil {
		calls.state = call.StateActive
	}
	return calls.error
}
func (calls *fakeCalls) Reject(context.Context) error {
	calls.operations = append(calls.operations, "reject")
	if calls.error == nil {
		calls.state = call.StateEnded
	}
	return calls.error
}
func (calls *fakeCalls) Hangup(context.Context) error {
	calls.operations = append(calls.operations, "hangup")
	if calls.sequence != nil {
		*calls.sequence = append(*calls.sequence, "hangup")
	}
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

type fakeEventRegistrar struct {
	callID       string
	sharedSecret []byte
	unregistered string
	err          error
}

func (registrar *fakeEventRegistrar) RegisterOutgoing(callID string, sharedSecret []byte) error {
	registrar.callID = callID
	registrar.sharedSecret = append([]byte(nil), sharedSecret...)
	return registrar.err
}

func (registrar *fakeEventRegistrar) Unregister(callID string) {
	registrar.unregistered = callID
}

type fakeCallMedia struct {
	callID       string
	offer        string
	answer       string
	negotiateErr error
	stopped      []string
	sequence     *[]string
}

type fakeMessages struct {
	destination string
	body        string
	reference   byte
	err         error
}

func (messages *fakeMessages) SendSMS(_ context.Context, destination string, body string, reference byte) error {
	messages.destination = destination
	messages.body = body
	messages.reference = reference
	return messages.err
}

func (media *fakeCallMedia) Negotiate(_ context.Context, callID string, offer string) (string, error) {
	media.callID = callID
	media.offer = offer
	if media.sequence != nil {
		*media.sequence = append(*media.sequence, "negotiate")
	}
	return media.answer, media.negotiateErr
}

func (media *fakeCallMedia) Stop(callID string) error {
	media.stopped = append(media.stopped, callID)
	return nil
}

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
		events    *fakeEventRegistrar
		media     *fakeCallMedia
		messages  *fakeMessages
		runtime   *Runtime
		sequence  []string
	)

	BeforeEach(func() {
		sequence = nil
		deviceKey, err := ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		senderKey, err = ecdh.X25519().GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		state = identity.State{
			Version: 2, DeviceUUID: deviceID, CommandPrivateKey: deviceKey.Bytes(), Credential: "device-secret",
		}
		signingKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
		remote = &fakeRemote{recipients: []incomingcall.Recipient{{
			InstallationID:        testInstallationID,
			IdentityPublicKey:     signingKey.Public().(ed25519.PublicKey),
			KeyAgreementPublicKey: bytes.Repeat([]byte{7}, 32),
		}}}
		calls = &fakeCalls{state: call.StateIdle, sequence: &sequence}
		events = &fakeEventRegistrar{}
		media = &fakeCallMedia{answer: "answer-sdp", sequence: &sequence}
		messages = &fakeMessages{}
		runtime, err = NewRuntime(remote, state, calls, time.Millisecond, Dependencies{Events: events, Media: media, Messages: messages})
		Expect(err).NotTo(HaveOccurred())
	})

	It("executes an encrypted SMS command without exposing its content", func() {
		messageID := "01900000-0000-7000-8000-000000000020"
		body := "你好，this message is longer than one short example " + strings.Repeat("文本", 80)
		payload := Payload{
			Version: 1, Operation: "message.send", MessageID: messageID,
			Number: "+8613500000000", Body: body,
		}
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, payload)

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		Expect(messages.destination).To(Equal("+8613500000000"))
		Expect(messages.body).To(Equal(body))
		expectedReference := sha256.Sum256([]byte(messageID))
		Expect(messages.reference).To(Equal(expectedReference[0]))
		Expect(openResult(sharedSecret, envelope, remote.result).Success).To(BeTrue())
		Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("13500000000"))
		Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("你好"))
	})

	It("returns an encrypted modem error for a rejected SMS", func() {
		messages.err = errors.New("+CMS ERROR: exposed modem detail")
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{
			Version: 1, Operation: "message.send", MessageID: "01900000-0000-7000-8000-000000000020",
			Number: "10086", Body: "hello",
		})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		result := openResult(sharedSecret, envelope, remote.result)
		Expect(result.Success).To(BeFalse())
		Expect(result.ErrorCode).To(Equal("modem_rejected"))
		Expect(string(remote.result.Ciphertext)).NotTo(ContainSubstring("CMS ERROR"))
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
		Entry("start", Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "+8613500000000", MediaOffer: "offer-sdp"}, "dial:+8613500000000"),
		Entry("answer", Payload{Version: 1, Operation: "call.answer", CallID: "call-1", MediaOffer: "offer-sdp"}, "answer"),
		Entry("reject", Payload{Version: 1, Operation: "call.reject", CallID: "call-1"}, "reject"),
		Entry("end", Payload{Version: 1, Operation: "call.end", CallID: "call-1"}, "hangup"),
		Entry("hold", Payload{Version: 1, Operation: "call.hold", CallID: "call-1"}, "hold"),
		Entry("resume", Payload{Version: 1, Operation: "call.resume", CallID: "call-1"}, "resume"),
		Entry("swap", Payload{Version: 1, Operation: "call.swap", CallID: "call-1"}, "swap"),
		Entry("DTMF", Payload{Version: 1, Operation: "call.dtmf", CallID: "call-1", Digits: "#"}, "dtmf:#"),
	)

	It("answers a waiting call through the modem call-waiting action", func() {
		calls.state = call.StateWaiting
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{
			Version: 1, Operation: "call.answer", CallID: "call-1", MediaOffer: "offer-sdp",
		})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		Expect(calls.operations).To(Equal([]string{"answer-waiting"}))
		Expect(openResult(sharedSecret, envelope, remote.result).MediaAnswer).To(Equal("answer-sdp"))
	})

	It("stops incoming media when the modem rejects an answer", func() {
		calls.state = call.StateRinging
		calls.error = errors.New("AT answer failed")
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{
			Version: 1, Operation: "call.answer", CallID: "call-1", MediaOffer: "offer-sdp",
		})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		result := openResult(sharedSecret, envelope, remote.result)
		Expect(result.Success).To(BeFalse())
		Expect(result.ErrorCode).To(Equal("modem_rejected"))
		Expect(media.stopped).To(Equal([]string{"call-1"}))
	})

	It("returns an encrypted generic modem error", func() {
		calls.error = errors.New("ATD exposed detail")
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086", MediaOffer: "offer-sdp"})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		result := openResult(sharedSecret, envelope, remote.result)
		Expect(result.Success).To(BeFalse())
		Expect(result.ErrorCode).To(Equal("modem_rejected"))
		Expect(string(remote.result.Ciphertext)).NotTo(ContainSubstring("ATD exposed detail"))
		Expect(media.stopped).To(Equal([]string{"call-1"}))
		Expect(events.unregistered).To(Equal("call-1"))
	})

	It("returns the encrypted SDP answer only after media negotiation and dialing succeed", func() {
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{
			Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086", MediaOffer: "offer-sdp",
		})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		result := openResult(sharedSecret, envelope, remote.result)
		Expect(result.Success).To(BeTrue())
		Expect(result.MediaAnswer).To(Equal("answer-sdp"))
		Expect(media.callID).To(Equal("call-1"))
		Expect(media.offer).To(Equal("offer-sdp"))
		Expect(calls.operations).To(Equal([]string{"dial:10086"}))
		Expect(sequence).To(Equal([]string{"dial", "negotiate"}))
	})

	It("hangs up a dial when media negotiation fails", func() {
		media.negotiateErr = errors.New("ALSA device exposed detail")
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{
			Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086", MediaOffer: "offer-sdp",
		})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		result := openResult(sharedSecret, envelope, remote.result)
		Expect(result.Success).To(BeFalse())
		Expect(result.ErrorCode).To(Equal("media_unavailable"))
		Expect(calls.operations).To(Equal([]string{"dial:10086", "hangup"}))
		Expect(sequence).To(Equal([]string{"dial", "negotiate", "hangup"}))
		Expect(events.unregistered).To(Equal("call-1"))
		Expect(string(remote.result.Ciphertext)).NotTo(ContainSubstring("ALSA"))
	})

	It("stops media after the modem accepts hangup", func() {
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.end", CallID: "call-1"})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		Expect(calls.operations).To(Equal([]string{"hangup"}))
		Expect(media.stopped).To(Equal([]string{"call-1"}))
	})

	It("round trips encrypted WebRTC negotiation without exposing SDP", func() {
		payload := Payload{
			Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086",
			MediaOffer: "v=0\r\na=fingerprint:sha-256 phone\r\n",
		}
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, payload)

		opened, openedSecret, err := open(state, envelope)

		Expect(err).NotTo(HaveOccurred())
		Expect(opened).To(Equal(payload))
		Expect(openedSecret).To(Equal(sharedSecret))
		Expect(string(envelope.Ciphertext)).NotTo(ContainSubstring("fingerprint"))

		encrypted, err := sealResult(bytes.NewReader(make([]byte, nonceBytes)), sharedSecret, envelope, Result{
			Version: 1, Success: true, CallState: call.StateDialing,
			MediaAnswer: "v=0\r\na=fingerprint:sha-256 device\r\n",
		})
		Expect(err).NotTo(HaveOccurred())
		result := openResult(sharedSecret, envelope, encrypted)
		Expect(result.MediaAnswer).To(ContainSubstring("fingerprint:sha-256 device"))
		Expect(string(encrypted.Ciphertext)).NotTo(ContainSubstring("fingerprint"))
	})

	It("registers the outgoing call secret before dialing", func() {
		registrar := &fakeEventRegistrar{}
		var err error
		runtime, err = NewRuntime(remote, state, calls, time.Millisecond, Dependencies{Events: registrar, Media: media, Messages: messages})
		Expect(err).NotTo(HaveOccurred())
		envelope, sharedSecret := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086", MediaOffer: "offer-sdp"})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		Expect(registrar.callID).To(Equal("call-1"))
		Expect(registrar.sharedSecret).To(Equal(sharedSecret))
		Expect(registrar.unregistered).To(BeEmpty())
	})

	It("unregisters the call secret when dialing fails", func() {
		registrar := &fakeEventRegistrar{}
		calls.error = errors.New("modem rejected")
		var err error
		runtime, err = NewRuntime(remote, state, calls, time.Millisecond, Dependencies{Events: registrar, Media: media, Messages: messages})
		Expect(err).NotTo(HaveOccurred())
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086", MediaOffer: "offer-sdp"})

		Expect(runtime.process(context.Background(), envelope)).To(Succeed())

		Expect(registrar.unregistered).To(Equal("call-1"))
	})

	It("fails closed when outgoing event registration fails", func() {
		registrar := &fakeEventRegistrar{err: errors.New("event key unavailable")}
		var err error
		runtime, err = NewRuntime(remote, state, calls, time.Millisecond, Dependencies{Events: registrar, Media: media, Messages: messages})
		Expect(err).NotTo(HaveOccurred())
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086", MediaOffer: "offer-sdp"})

		Expect(runtime.process(context.Background(), envelope)).To(MatchError(ContainSubstring("register call events")))
		Expect(calls.operations).To(BeEmpty())
		Expect(remote.commandID).To(BeEmpty())
	})

	It("rejects unsupported or malformed plaintext without invoking the modem", func() {
		for _, payload := range []Payload{
			{Version: 2, Operation: "call.end", CallID: "call-1"},
			{Version: 1, Operation: "call.end"},
			{Version: 1, Operation: "call.start", CallID: "call-1", Number: "10086"},
			{Version: 1, Operation: "call.answer", MediaOffer: "offer-sdp"},
			{Version: 1, Operation: "call.reject"},
			{Version: 1, Operation: "call.hold"},
			{Version: 1, Operation: "call.resume"},
			{Version: 1, Operation: "call.swap"},
			{Version: 1, Operation: "call.dtmf", CallID: "call-1"},
			{Version: 1, Operation: "message.send", Number: "10086", Body: "hello"},
			{Version: 1, Operation: "message.send", MessageID: "not-a-uuid", Number: "10086", Body: "hello"},
			{Version: 1, Operation: "message.send", MessageID: "01900000-0000-7000-8000-000000000020", Body: "hello"},
			{Version: 1, Operation: "message.send", MessageID: "01900000-0000-7000-8000-000000000020", Number: "10086"},
			{Version: 1, Operation: "unknown", CallID: "call-1"},
		} {
			envelope, sharedSecret := sealCommand(state, senderKey, commandID, payload)
			Expect(runtime.process(context.Background(), envelope)).To(Succeed())
			Expect(openResult(sharedSecret, envelope, remote.result).ErrorCode).To(Equal("invalid_command"))
		}
		Expect(calls.operations).To(BeEmpty())
	})

	It("rejects a command addressed to another physical slot or SIM", func() {
		state.SlotID = "01900000-0000-7000-8000-000000000002"
		state.SIMID = "01900000-0000-7000-8000-000000000003"
		var runtimeErr error
		runtime, runtimeErr = NewRuntime(remote, state, calls, time.Millisecond, Dependencies{Events: events, Media: media, Messages: messages})
		Expect(runtimeErr).NotTo(HaveOccurred())

		for _, payload := range []Payload{
			{Version: 1, Operation: "message.send", MessageID: "01900000-0000-7000-8000-000000000020", Number: "13500000000", Body: "wrong slot", SlotID: "01900000-0000-7000-8000-000000000004"},
			{Version: 1, Operation: "message.send", MessageID: "01900000-0000-7000-8000-000000000020", Number: "13500000000", Body: "wrong SIM", SIMID: "01900000-0000-7000-8000-000000000005"},
		} {
			envelope, sharedSecret := sealCommand(state, senderKey, commandID, payload)
			Expect(runtime.process(context.Background(), envelope)).To(Succeed())
			Expect(openResult(sharedSecret, envelope, remote.result).ErrorCode).To(Equal("wrong_sim_slot"))
		}
		Expect(messages.destination).To(BeEmpty())
	})

	It("rejects tampering before invoking the modem or acknowledging", func() {
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.end", CallID: "call-1"})
		envelope.Ciphertext[0] ^= 0xff

		err := runtime.process(context.Background(), envelope)

		Expect(err).To(MatchError(ContainSubstring("verify command")))
		Expect(calls.operations).To(BeEmpty())
		Expect(remote.commandID).To(BeEmpty())
	})

	It("rejects commands from unapproved or unavailable installation identities", func() {
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{Version: 1, Operation: "call.end", CallID: "call-1"})
		remote.recipients = nil
		Expect(runtime.process(context.Background(), envelope)).To(MatchError(ContainSubstring("verify command")))
		Expect(calls.operations).To(BeEmpty())

		remote.recipientError = errors.New("server unavailable")
		Expect(runtime.process(context.Background(), envelope)).To(MatchError(ContainSubstring("load trusted command senders")))
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

	It("claims, executes, and completes a command before continuing to poll", func() {
		envelope, _ := sealCommand(state, senderKey, commandID, Payload{
			Version: 1, Operation: "message.send", MessageID: "01900000-0000-7000-8000-000000000020",
			Number: "10086", Body: "runtime delivery",
		})
		remote.claims = []*Envelope{&envelope}
		remote.completed = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-remote.completed
			cancel()
		}()

		Expect(runtime.Run(ctx)).To(MatchError(context.Canceled))

		Expect(messages.destination).To(Equal("10086"))
		Expect(messages.body).To(Equal("runtime delivery"))
		Expect(remote.commandID).To(Equal(commandID))
	})

	It("validates runtime dependencies", func() {
		dependencies := Dependencies{Events: events, Media: media, Messages: messages}
		_, err := NewRuntime(nil, state, calls, time.Second, dependencies)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, identity.State{}, calls, time.Second, dependencies)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, nil, time.Second, dependencies)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, calls, 0, dependencies)
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, calls, time.Second, Dependencies{Media: media, Messages: messages})
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, calls, time.Second, Dependencies{Events: events, Messages: messages})
		Expect(err).To(HaveOccurred())
		_, err = NewRuntime(remote, state, calls, time.Second, Dependencies{Events: events, Media: media})
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
	now := time.Now().UTC().Truncate(time.Millisecond)
	envelope := Envelope{
		ID: commandID, DeviceUUID: state.DeviceUUID, SenderInstallationID: testInstallationID,
		SenderPublicKey: senderKey.PublicKey().Bytes(), Nonce: nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, associatedData("sim-deck-command-v1", commandID, state.DeviceUUID)),
		CreatedAt:  now, ExpiresAt: now.Add(time.Minute),
	}
	signingKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	envelope.Signature = ed25519.Sign(signingKey, commandSignedData(envelope))
	return envelope, sharedSecret
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
