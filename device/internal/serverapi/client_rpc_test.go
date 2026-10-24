package serverapi

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/radio"
)

type fakeDeviceRPC struct {
	turn      *communicationv1.FetchTurnCredentialsResponse
	radio     *communicationv1.UpdateRadioStatusRequest
	identity  *communicationv1.UpdateSimIdentityResponse
	command   *communicationv1.ClaimCommandResponse
	discovery *communicationv1.SubmitDiscoveryResponse
	status    *communicationv1.GetDiscoveryStatusResponse
	err       error
}

func (fake *fakeDeviceRPC) FetchTurnCredentials(context.Context, *communicationv1.FetchTurnCredentialsRequest) (*communicationv1.FetchTurnCredentialsResponse, error) {
	return fake.turn, fake.err
}
func (fake *fakeDeviceRPC) UpdateRadioStatus(_ context.Context, request *communicationv1.UpdateRadioStatusRequest) (*communicationv1.UpdateRadioStatusResponse, error) {
	fake.radio = request
	return &communicationv1.UpdateRadioStatusResponse{}, fake.err
}
func (fake *fakeDeviceRPC) UpdateSimIdentity(context.Context, *communicationv1.UpdateSimIdentityRequest) (*communicationv1.UpdateSimIdentityResponse, error) {
	return fake.identity, fake.err
}
func (fake *fakeDeviceRPC) ListInstallationRecipients(context.Context, *communicationv1.ListInstallationRecipientsRequest) (*communicationv1.ListInstallationRecipientsResponse, error) {
	return &communicationv1.ListInstallationRecipientsResponse{}, fake.err
}
func (fake *fakeDeviceRPC) UploadIncomingCall(context.Context, *communicationv1.UploadIncomingCallRequest) (*communicationv1.UploadIncomingCallResponse, error) {
	return &communicationv1.UploadIncomingCallResponse{}, fake.err
}
func (fake *fakeDeviceRPC) ClaimCommand(context.Context, *communicationv1.ClaimCommandRequest) (*communicationv1.ClaimCommandResponse, error) {
	return fake.command, fake.err
}
func (fake *fakeDeviceRPC) CompleteCommand(context.Context, *communicationv1.CompleteCommandRequest) (*communicationv1.CompleteCommandResponse, error) {
	return &communicationv1.CompleteCommandResponse{}, fake.err
}
func (fake *fakeDeviceRPC) UpdateCommandKey(context.Context, *communicationv1.UpdateCommandKeyRequest) (*communicationv1.UpdateCommandKeyResponse, error) {
	return &communicationv1.UpdateCommandKeyResponse{}, fake.err
}
func (fake *fakeDeviceRPC) SubmitDiscovery(context.Context, *communicationv1.SubmitDiscoveryRequest) (*communicationv1.SubmitDiscoveryResponse, error) {
	return fake.discovery, fake.err
}
func (fake *fakeDeviceRPC) GetDiscoveryStatus(context.Context, *communicationv1.GetDiscoveryStatusRequest) (*communicationv1.GetDiscoveryStatusResponse, error) {
	return fake.status, fake.err
}

var _ = Describe("Device RPC client", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var client *Client

	BeforeEach(func() {
		client = NewClient("https://deck.example", &http.Client{})
	})

	It("maps TURN credentials and rejects expired responses", func() {
		client.deviceClient = &fakeDeviceRPC{turn: &communicationv1.FetchTurnCredentialsResponse{Urls: []string{"turn:example:3478?transport=udp"}, Username: "u", Credential: "c", ExpiresAt: timestamppb.New(time.Now().Add(time.Hour))}}
		servers, err := client.FetchTURNCredentials(context.Background(), identity.State{DeviceUUID: deviceID, Credential: "secret"})
		Expect(err).NotTo(HaveOccurred())
		Expect(servers[0].URLs).To(Equal([]string{"turn:example:3478?transport=udp"}))
		client.deviceClient = &fakeDeviceRPC{turn: &communicationv1.FetchTurnCredentialsResponse{ExpiresAt: timestamppb.New(time.Now().Add(-time.Minute))}}
		_, err = client.FetchTURNCredentials(context.Background(), identity.State{DeviceUUID: deviceID, Credential: "secret"})
		Expect(err).To(MatchError("server returned invalid TURN credentials"))
	})

	It("sends authenticated radio status through the generated RPC", func() {
		fake := &fakeDeviceRPC{}
		client.deviceClient = fake
		bars := 4
		err := client.UpdateRadioStatus(context.Background(), identity.State{DeviceUUID: deviceID, Credential: "secret", SlotKey: "slot", SlotIndex: 1}, radio.Status{RadioAccessTechnology: "lte", SignalBars: &bars})
		Expect(err).NotTo(HaveOccurred())
		Expect(fake.radio.GetDeviceUuid()).To(Equal(deviceID))
		Expect(fake.radio.GetSignalBars()).To(Equal(int32(4)))
	})

	It("maps SIM assignment and validates server identity", func() {
		client.deviceClient = &fakeDeviceRPC{identity: &communicationv1.UpdateSimIdentityResponse{SlotId: deviceID, SimId: "01900000-0000-7000-8000-000000000002", NumericId: 7}}
		assignment, err := client.UpdateSIMIdentity(context.Background(), identity.State{DeviceUUID: deviceID, Credential: "secret", SlotKey: "slot", SlotIndex: 1, SIMFingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
		Expect(err).NotTo(HaveOccurred())
		Expect(assignment.NumericID).To(Equal(7))
	})

	It("decodes an opaque command and handles an empty queue", func() {
		client.deviceClient = &fakeDeviceRPC{command: &communicationv1.ClaimCommandResponse{HasCommand: false}}
		commandEnvelope, err := client.Claim(context.Background(), deviceID, "secret")
		Expect(err).NotTo(HaveOccurred())
		Expect(commandEnvelope).To(BeNil())
		client.deviceClient = &fakeDeviceRPC{command: &communicationv1.ClaimCommandResponse{HasCommand: true, Command: &communicationv1.EncryptedCommand{Id: deviceID, DeviceUuid: deviceID, SenderInstallationId: deviceID, SenderPublicKey: make([]byte, 32), Nonce: make([]byte, 12), Ciphertext: []byte{1}, Signature: make([]byte, ed25519.SignatureSize), CreatedAt: timestamppb.New(time.Now()), ExpiresAt: timestamppb.New(time.Now().Add(time.Minute))}}}
		commandEnvelope, err = client.Claim(context.Background(), deviceID, "secret")
		Expect(err).NotTo(HaveOccurred())
		Expect(commandEnvelope.ID).To(Equal(deviceID))
	})

	It("maps RPC failures without leaking protocol details", func() {
		client.deviceClient = &fakeDeviceRPC{err: errors.New("invalid_device_credential")}
		err := client.UpdateCommandKey(context.Background(), identity.State{DeviceUUID: deviceID, Credential: "secret"})
		Expect(err).To(MatchError(ContainSubstring("invalid_device_credential")))
	})

})
