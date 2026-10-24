package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sim-deck-server/internal/auth"
	"sim-deck-server/internal/domain"
	"sim-deck-server/internal/store"
)

var _ = Describe("Connect business services", func() {
	It("serves owner device inventory only through the RPC route", func() {
		repository := &fakeRepository{ownerID: "owner-1", devices: []store.DeviceRecord{{ID: "01900000-0000-7000-8000-000000000001", NumericID: 7}}}
		handler := Handler(repository, Config{Now: time.Now})
		rest := httptest.NewRecorder()
		restRequest := httptest.NewRequest(http.MethodGet, "/v1/devices", nil)
		restRequest.Header.Set("Authorization", "Bearer owner-token")
		handler.ServeHTTP(rest, restRequest)
		Expect(rest.Code).To(Equal(http.StatusNotFound))

		rpc := httptest.NewRecorder()
		rpcRequest := httptest.NewRequest(http.MethodPost, "/relay.communication.v1.OwnerService/ListDevices", strings.NewReader(`{}`))
		rpcRequest.Header.Set("Content-Type", "application/json")
		rpcRequest.Header.Set("Authorization", "Bearer owner-token")
		handler.ServeHTTP(rpc, rpcRequest)
		Expect(rpc.Code).To(Equal(http.StatusOK))
		Expect(repository.ownerAuthToken).To(Equal("owner-token"))
	})

	It("rejects device RPCs without a device credential", func() {
		repository := &fakeRepository{}
		handler := Handler(repository, Config{Now: time.Now})
		request := httptest.NewRequest(http.MethodPost, "/relay.communication.v1.DeviceService/UpdateRadioStatus", strings.NewReader(`{"deviceUuid":"01900000-0000-7000-8000-000000000001"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
	})

	It("submits signed device discovery through the Connect service", func() {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		pendingCredential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		deviceID := "01900000-0000-7000-8000-000000000001"
		commandKey := make([]byte, commandPublicKeyBytes)
		fingerprint := strings.Repeat("ab", 32)
		proof := deviceDiscoverySignedDataWithSlot(deviceID, publicKey, commandKey, pendingCredential, "usb-serial-1", 1, fingerprint)
		now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
		repository := &fakeRepository{discoveryRecord: store.DeviceDiscoveryRecord{
			DeviceUUID: deviceID, IdentityPublicKey: publicKey, CommandPublicKey: commandKey,
			Status: store.DeviceDiscoveryPending, FirstSeenAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Minute),
			SlotKey: "usb-serial-1", SlotIndex: 1,
		}}
		handler := Handler(repository, Config{Now: func() time.Time { return now }})
		request := &communicationv1.SubmitDiscoveryRequest{
			DeviceUuid: deviceID, SlotKey: "usb-serial-1", SlotIndex: 1, SimFingerprint: fingerprint,
			IdentityPublicKey: publicKey, CommandPublicKey: commandKey, PendingCredential: pendingCredential,
			Signature: ed25519.Sign(privateKey, proof),
		}
		rawResponse := invokeDeviceRPC(handler, "SubmitDiscovery", request, "")
		Expect(rawResponse.Code).To(Equal(http.StatusOK))
		response := new(communicationv1.SubmitDiscoveryResponse)
		Expect(protojson.Unmarshal(rawResponse.Body.Bytes(), response)).To(Succeed())
		Expect(response.GetStatus()).To(Equal(string(store.DeviceDiscoveryPending)))
		Expect(response.GetSlotKey()).To(Equal("usb-serial-1"))
		Expect(repository.discoveryRequest.DeviceUUID).To(Equal(deviceID))
		Expect(repository.discoveryRequest.SlotIndex).To(Equal(1))
		Expect(repository.discoveryRequest.SIMFingerprint).To(Equal(bytesOf(0xab, 32)))
		expectedHash, err := auth.TokenHash(pendingCredential)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.discoveryRequest.CredentialHash).To(Equal(expectedHash[:]))
	})

	It("rejects a discovery with an invalid identity proof before storage", func() {
		publicKey, _, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		pendingCredential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		repository := &fakeRepository{}
		handler := Handler(repository, Config{Now: time.Now})
		rawResponse := invokeDeviceRPC(handler, "SubmitDiscovery", &communicationv1.SubmitDiscoveryRequest{
			DeviceUuid: "01900000-0000-7000-8000-000000000001", SlotKey: "1", SlotIndex: 1,
			IdentityPublicKey: publicKey, CommandPublicKey: make([]byte, commandPublicKeyBytes),
			PendingCredential: pendingCredential, Signature: make([]byte, ed25519.SignatureSize),
		}, "")
		Expect(rawResponse.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.discoverySubmittedAt).To(BeZero())
	})

	It("maps a closed discovery window to an explicit RPC conflict", func() {
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		pendingCredential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		deviceID := "01900000-0000-7000-8000-000000000001"
		commandKey := make([]byte, commandPublicKeyBytes)
		proof := deviceDiscoverySignedData(deviceID, publicKey, commandKey, pendingCredential)
		repository := &fakeRepository{submitDiscoveryError: domain.ErrDeviceDiscoveryClosed}
		handler := Handler(repository, Config{Now: time.Now})
		rawResponse := invokeDeviceRPC(handler, "SubmitDiscovery", &communicationv1.SubmitDiscoveryRequest{
			DeviceUuid: deviceID, IdentityPublicKey: publicKey, CommandPublicKey: commandKey,
			PendingCredential: pendingCredential, Signature: ed25519.Sign(privateKey, proof),
		}, "")
		Expect(rawResponse.Code).To(Equal(http.StatusConflict))
	})

	It("reads device discovery status using the pending credential over RPC", func() {
		pendingCredential := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		now := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
		repository := &fakeRepository{discoveryRecord: store.DeviceDiscoveryRecord{DeviceUUID: "01900000-0000-7000-8000-000000000001", Status: store.DeviceDiscoveryApproved, FirstSeenAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), NumericID: 12}}
		handler := Handler(repository, Config{Now: func() time.Time { return now }})
		rawResponse := invokeDeviceRPC(handler, "GetDiscoveryStatus", &communicationv1.GetDiscoveryStatusRequest{DeviceUuid: repository.discoveryRecord.DeviceUUID}, pendingCredential)
		Expect(rawResponse.Code).To(Equal(http.StatusOK))
		response := new(communicationv1.GetDiscoveryStatusResponse)
		Expect(protojson.Unmarshal(rawResponse.Body.Bytes(), response)).To(Succeed())
		Expect(response.GetStatus()).To(Equal(string(store.DeviceDiscoveryApproved)))
		expectedHash, err := auth.TokenHash(pendingCredential)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.discoveryCredentialHash).To(Equal(expectedHash[:]))
	})

	It("pairs a device directly through the Connect repository operation", func() {
		pairingToken := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
		deviceCredential := base64.RawURLEncoding.EncodeToString(bytesOf(1, 32))
		deviceID := "01900000-0000-7000-8000-000000000001"
		repository := &fakeRepository{pairingRecord: store.DeviceRecord{ID: deviceID, NumericID: 9}}
		handler := Handler(repository, Config{Now: time.Now})
		rawResponse := invokeDeviceRPC(handler, "PairDevice", &communicationv1.PairDeviceRequest{
			PairingToken: pairingToken, DeviceUuid: deviceID, ProfileCiphertext: []byte("encrypted-profile"),
			IdentityPublicKey: make([]byte, ed25519.PublicKeySize), CommandPublicKey: make([]byte, commandPublicKeyBytes),
			WrappedVaultKey: []byte("wrapped-key"), DeviceCredential: deviceCredential,
		}, "")
		Expect(rawResponse.Code).To(Equal(http.StatusOK))
		response := new(communicationv1.PairDeviceResponse)
		Expect(protojson.Unmarshal(rawResponse.Body.Bytes(), response)).To(Succeed())
		Expect(response.GetDeviceUuid()).To(Equal(deviceID))
		Expect(response.GetNumericId()).To(Equal(int32(9)))
		expectedTokenHash, err := auth.TokenHash(pairingToken)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.pairingTokenConsumedHash).To(Equal(expectedTokenHash[:]))
		expectedCredentialHash, err := auth.TokenHash(deviceCredential)
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.pairing.CredentialHash).To(Equal(expectedCredentialHash[:]))
		Expect(repository.pairing.ProfileCiphertext).To(Equal([]byte("encrypted-profile")))
	})

	It("returns unauthenticated when a pairing token has already been consumed", func() {
		repository := &fakeRepository{pairingError: domain.ErrInvalidPairingToken}
		handler := Handler(repository, Config{Now: time.Now})
		rawResponse := invokeDeviceRPC(handler, "PairDevice", &communicationv1.PairDeviceRequest{
			PairingToken: base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
			DeviceUuid:   "01900000-0000-7000-8000-000000000001", IdentityPublicKey: make([]byte, ed25519.PublicKeySize),
			CommandPublicKey: make([]byte, commandPublicKeyBytes), DeviceCredential: base64.RawURLEncoding.EncodeToString(bytesOf(1, 32)),
		}, "")
		Expect(rawResponse.Code).To(Equal(http.StatusUnauthorized))
	})

	It("rejects legacy business HTTP endpoints", func() {
		handler := Handler(&fakeRepository{}, Config{Now: time.Now})
		for _, path := range []string{"/v1/devices", "/v1/device-discoveries", "/v1/device-pairings", "/v1/auth/passkey", "/v1/auth/passkey/login/options"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			Expect(response.Code).To(Equal(http.StatusNotFound), path)
		}
	})
})

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func invokeDeviceRPC(handler http.Handler, method string, request proto.Message, bearer string) *httptest.ResponseRecorder {
	body, err := protojson.Marshal(request)
	if err != nil {
		panic(err)
	}
	rawRequest := httptest.NewRequest(http.MethodPost, "/relay.communication.v1.DeviceService/"+method, strings.NewReader(string(body)))
	rawRequest.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		rawRequest.Header.Set("Authorization", "Bearer "+bearer)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rawRequest)
	return response
}
