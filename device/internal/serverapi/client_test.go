package serverapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/callevent"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/incomingcall"
	"sim-deck-device/internal/radio"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func clientWithHandler(handler roundTripFunc) *Client {
	return NewClient("https://deck.example", &http.Client{Transport: handler})
}

var _ = Describe("TURN credential client", func() {
	It("requests relay-only UDP and TCP addresses with the bound device credential", func() {
		state := identity.State{
			DeviceUUID: "01900000-0000-7000-8000-000000000001",
			Credential: "device-secret",
		}
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/v1/devices/" + state.DeviceUUID + "/media/turn-credentials"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			return response(http.StatusOK, `{"urls":["turn:turn.example.com:3478?transport=udp","turn:turn.example.com:3478?transport=tcp"],"username":"user","credential":"password","expires_at":"2099-01-01T00:00:00Z"}`), nil
		})

		servers, err := client.FetchTURNCredentials(context.Background(), state)

		Expect(err).NotTo(HaveOccurred())
		Expect(servers).To(HaveLen(1))
		Expect(servers[0].URLs).To(Equal([]string{
			"turn:turn.example.com:3478?transport=udp",
			"turn:turn.example.com:3478?transport=tcp",
		}))
		Expect(servers[0].Username).To(Equal("user"))
	})

	It("rejects missing device identity and malformed credentials", func() {
		_, err := clientWithHandler(func(*http.Request) (*http.Response, error) {
			Fail("request should not be sent without a bound identity")
			return nil, nil
		}).FetchTURNCredentials(context.Background(), identity.State{})
		Expect(err).To(MatchError("bound device identity is required"))

		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"urls":[],"username":"user","credential":"password","expires_at":"2099-01-01T00:00:00Z"}`), nil
		})
		_, err = client.FetchTURNCredentials(context.Background(), identity.State{
			DeviceUUID: "01900000-0000-7000-8000-000000000001",
			Credential: "device-secret",
		})
		Expect(err).To(MatchError("server returned invalid TURN credentials"))
	})
})

var _ = Describe("Installation recipient client", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"

	It("lists approved public encryption identities with device authentication", func() {
		recipients := []InstallationRecipient{{
			InstallationID:        "01900000-0000-7000-8000-000000000101",
			IdentityPublicKey:     bytes.Repeat([]byte{6}, ed25519.PublicKeySize),
			KeyAgreementPublicKey: bytes.Repeat([]byte{7}, 32),
		}}
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/v1/device-installations"))
			Expect(request.URL.Query().Get("device_uuid")).To(Equal(deviceID))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			body, err := json.Marshal(map[string]any{"installations": recipients})
			Expect(err).NotTo(HaveOccurred())
			return response(http.StatusOK, string(body)), nil
		})

		listed, err := client.ListInstallationRecipients(context.Background(), deviceID, "device-secret")

		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(Equal(recipients))
	})

	It("returns an initialized empty list when no iPhone is approved", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{}`), nil
		})

		listed, err := client.ListInstallationRecipients(context.Background(), deviceID, "device-secret")

		Expect(err).NotTo(HaveOccurred())
		Expect(listed).To(BeEmpty())
		Expect(listed).NotTo(BeNil())
	})

	DescribeTable("rejects malformed recipient metadata",
		func(installationID string, identityPublicKey []byte, agreementPublicKey []byte) {
			body, err := json.Marshal(map[string]any{"installations": []InstallationRecipient{{
				InstallationID: installationID, IdentityPublicKey: identityPublicKey, KeyAgreementPublicKey: agreementPublicKey,
			}}})
			Expect(err).NotTo(HaveOccurred())
			client := clientWithHandler(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, string(body)), nil
			})

			_, err = client.ListInstallationRecipients(context.Background(), deviceID, "device-secret")

			Expect(err).To(MatchError("server returned an invalid installation recipient"))
		},
		Entry("invalid installation UUID", "not-a-uuid", bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32)),
		Entry("invalid Ed25519 key", "01900000-0000-7000-8000-000000000101", bytes.Repeat([]byte{7}, 31), bytes.Repeat([]byte{8}, 32)),
		Entry("invalid X25519 key", "01900000-0000-7000-8000-000000000101", bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 31)),
	)

	It("maps malformed responses and request, transport, and server failures", func() {
		malformed := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, "not-json"), nil
		})
		_, err := malformed.ListInstallationRecipients(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("decode installation recipients")))

		_, err = NewClient("://invalid", nil).ListInstallationRecipients(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("create installation recipient request")))

		transportFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		_, err = transportFailure.ListInstallationRecipients(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("list installation recipients")))

		serverFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_device_credential","message":"sensitive detail"}}`), nil
		})
		_, err = serverFailure.ListInstallationRecipients(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("invalid_device_credential")))
		Expect(err.Error()).NotTo(ContainSubstring("sensitive detail"))
	})
})

var _ = Describe("Incoming call client", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	var notification incomingcall.Notification

	BeforeEach(func() {
		notification = incomingcall.Notification{
			ID:         "01900000-0000-7000-8000-000000000010",
			Type:       "call.incoming",
			DeviceUUID: deviceID,
			OccurredAt: time.Date(2026, 10, 16, 8, 9, 10, 0, time.UTC),
			KeyEpoch:   1,
			Recipients: []incomingcall.RecipientEnvelope{{
				InstallationID:  "01900000-0000-7000-8000-000000000101",
				SenderPublicKey: bytes.Repeat([]byte{1}, 32),
				Nonce:           bytes.Repeat([]byte{2}, 12),
				Ciphertext:      []byte("opaque"),
				Signature:       bytes.Repeat([]byte{3}, 64),
			}},
		}
	})

	DescribeTable("uploads encrypted fanout with device authentication",
		func(status int) {
			client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
				Expect(request.Method).To(Equal(http.MethodPost))
				Expect(request.URL.Path).To(Equal("/v1/incoming-calls"))
				Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
				Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
				var body struct {
					Notification incomingcall.Notification `json:"notification"`
				}
				Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
				Expect(body.Notification).To(Equal(notification))
				return response(status, `{}`), nil
			})

			Expect(client.UploadIncomingCall(context.Background(), deviceID, "device-secret", notification)).To(Succeed())
		},
		Entry("new notification", http.StatusCreated),
		Entry("idempotent retry", http.StatusOK),
	)

	It("rejects a mismatched device before making a request", func() {
		Expect(NewClient("http://unused.invalid", nil).UploadIncomingCall(
			context.Background(), "another-device", "device-secret", notification,
		)).To(MatchError("incoming call device does not match the authenticated device"))
	})

	It("maps request, transport, and server failures without response details", func() {
		Expect(NewClient("://invalid", nil).UploadIncomingCall(
			context.Background(), deviceID, "device-secret", notification,
		)).To(MatchError(ContainSubstring("create incoming call request")))

		transportFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		Expect(transportFailure.UploadIncomingCall(
			context.Background(), deviceID, "device-secret", notification,
		)).To(MatchError(ContainSubstring("upload incoming call")))

		serverFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusConflict, `{"error":{"code":"untrusted_installation_recipient","message":"sensitive detail"}}`), nil
		})
		err := serverFailure.UploadIncomingCall(context.Background(), deviceID, "device-secret", notification)
		Expect(err).To(MatchError(ContainSubstring("untrusted_installation_recipient")))
		Expect(err.Error()).NotTo(ContainSubstring("sensitive detail"))
	})
})

var _ = Describe("Radio status client", func() {
	It("uploads authenticated modem telemetry", func() {
		state := identity.State{
			DeviceUUID: "01900000-0000-7000-8000-000000000001",
			Credential: "device-secret",
			SlotKey:    "primary",
			SlotIndex:  1,
		}
		bars := 3
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPut))
			Expect(request.URL.Path).To(Equal("/v1/device-radio-status"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			var body struct {
				DeviceUUID            string `json:"device_uuid"`
				SlotKey               string `json:"slot_key"`
				SlotIndex             int    `json:"slot_index"`
				RadioAccessTechnology string `json:"radio_access_technology"`
				SignalBars            *int   `json:"signal_bars"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.SlotKey).To(Equal(state.SlotKey))
			Expect(body.SlotIndex).To(Equal(state.SlotIndex))
			Expect(body.RadioAccessTechnology).To(Equal("lte"))
			Expect(body.SignalBars).NotTo(BeNil())
			Expect(*body.SignalBars).To(Equal(3))
			return response(http.StatusNoContent, ""), nil
		})

		Expect(client.UpdateRadioStatus(context.Background(), state, radio.Status{
			RadioAccessTechnology: "lte",
			SignalBars:            &bars,
		})).To(Succeed())
	})

	It("maps transport and server failures", func() {
		state := identity.State{DeviceUUID: "device", Credential: "secret"}
		status := radio.Status{RadioAccessTechnology: "unknown"}
		transportFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		Expect(transportFailure.UpdateRadioStatus(context.Background(), state, status)).To(MatchError(ContainSubstring("update radio status")))

		serverFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_device_credential"}}`), nil
		})
		Expect(serverFailure.UpdateRadioStatus(context.Background(), state, status)).To(MatchError(ContainSubstring("invalid_device_credential")))
	})
})

var _ = Describe("SIM identity client", func() {
	var state identity.State

	BeforeEach(func() {
		state = identity.State{
			DeviceUUID:     "01900000-0000-7000-8000-000000000001",
			Credential:     "device-secret",
			SlotID:         "01900000-0000-7000-8000-000000000002",
			SlotKey:        "primary",
			SlotIndex:      1,
			SIMFingerprint: strings.Repeat("a", 64),
		}
	})

	It("synchronizes the current card with authenticated slot metadata", func() {
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPut))
			Expect(request.URL.Path).To(Equal("/v1/device-sim-identity"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
			var body struct {
				DeviceUUID     string `json:"device_uuid"`
				SlotKey        string `json:"slot_key"`
				SlotIndex      int    `json:"slot_index"`
				SIMFingerprint string `json:"sim_fingerprint"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.SlotKey).To(Equal(state.SlotKey))
			Expect(body.SlotIndex).To(Equal(state.SlotIndex))
			Expect(body.SIMFingerprint).To(Equal(state.SIMFingerprint))
			return response(http.StatusOK, `{"sim_id":"01900000-0000-7000-8000-000000000003","numeric_id":7}`), nil
		})

		simID, numericID, err := client.UpdateSIMIdentity(context.Background(), state)

		Expect(err).NotTo(HaveOccurred())
		Expect(simID).To(Equal("01900000-0000-7000-8000-000000000003"))
		Expect(numericID).To(Equal(7))
	})

	It("rejects incomplete slot identity before sending a request", func() {
		state.SIMFingerprint = ""

		_, _, err := NewClient("http://unused.invalid", nil).UpdateSIMIdentity(context.Background(), state)

		Expect(err).To(MatchError("bound slot and SIM fingerprint are required"))
	})

	It("maps request, transport, server, and response failures", func() {
		_, _, err := NewClient("://invalid", nil).UpdateSIMIdentity(context.Background(), state)
		Expect(err).To(MatchError(ContainSubstring("create SIM identity request")))

		transportFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		_, _, err = transportFailure.UpdateSIMIdentity(context.Background(), state)
		Expect(err).To(MatchError(ContainSubstring("synchronize SIM identity")))

		serverFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_device_credential","message":"sensitive detail"}}`), nil
		})
		_, _, err = serverFailure.UpdateSIMIdentity(context.Background(), state)
		Expect(err).To(MatchError(ContainSubstring("invalid_device_credential")))
		Expect(err.Error()).NotTo(ContainSubstring("sensitive detail"))

		malformed := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, "not-json"), nil
		})
		_, _, err = malformed.UpdateSIMIdentity(context.Background(), state)
		Expect(err).To(MatchError(ContainSubstring("decode SIM identity response")))

		invalidAssignment := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{"sim_id":"invalid","numeric_id":100}`), nil
		})
		_, _, err = invalidAssignment.UpdateSIMIdentity(context.Background(), state)
		Expect(err).To(MatchError("server returned an invalid SIM assignment"))
	})
})

var _ = Describe("Server discovery client", func() {
	var state identity.State

	BeforeEach(func() {
		var err error
		state, err = identity.NewStore(GinkgoT().TempDir(), "01900000-0000-7000-8000-000000000001").LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
	})

	It("submits a signed discovery request without exposing private keys", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.Path).To(Equal("/v1/device-discoveries"))
			var body struct {
				DeviceUUID        string `json:"device_uuid"`
				IdentityPublicKey []byte `json:"identity_public_key"`
				CommandPublicKey  []byte `json:"command_public_key"`
				PendingCredential string `json:"pending_credential"`
				Signature         []byte `json:"signature"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.IdentityPublicKey).To(Equal([]byte(state.PublicKey())))
			Expect(body.IdentityPublicKey).NotTo(Equal([]byte(state.IdentityPrivateKey)))
			Expect(body.CommandPublicKey).To(Equal(state.CommandPublicKey()))
			Expect(body.PendingCredential).To(Equal(credential))
			proof := discoverySignedData(body.DeviceUUID, body.IdentityPublicKey, body.CommandPublicKey, body.PendingCredential)
			Expect(ed25519.Verify(body.IdentityPublicKey, proof, body.Signature)).To(BeTrue())
			return response(http.StatusAccepted, `{"discovery":{"device_uuid":"`+state.DeviceUUID+`","status":"pending"}}`), nil
		})

		result, err := client.SubmitDiscovery(context.Background(), credential, state)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(DiscoveryResult{DeviceUUID: state.DeviceUUID, Status: DiscoveryPending}))
	})

	It("binds slot metadata into the signed discovery proof", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
		state.SlotKey = "primary"
		state.SlotIndex = 1
		state.SIMFingerprint = strings.Repeat("a", 64)
		const slotID = "01900000-0000-7000-8000-000000000002"
		const simID = "01900000-0000-7000-8000-000000000003"
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			var body struct {
				DeviceUUID        string `json:"device_uuid"`
				IdentityPublicKey []byte `json:"identity_public_key"`
				CommandPublicKey  []byte `json:"command_public_key"`
				PendingCredential string `json:"pending_credential"`
				SlotKey           string `json:"slot_key"`
				SlotIndex         int    `json:"slot_index"`
				SIMFingerprint    string `json:"sim_fingerprint"`
				Signature         []byte `json:"signature"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.SlotKey).To(Equal("primary"))
			Expect(body.SlotIndex).To(Equal(1))
			Expect(body.SIMFingerprint).To(Equal(state.SIMFingerprint))
			proof := discoverySignedDataWithSlot(
				body.DeviceUUID,
				body.IdentityPublicKey,
				body.CommandPublicKey,
				body.PendingCredential,
				body.SlotKey,
				body.SlotIndex,
				body.SIMFingerprint,
			)
			Expect(ed25519.Verify(body.IdentityPublicKey, proof, body.Signature)).To(BeTrue())
			return response(http.StatusAccepted, `{"discovery":{"device_uuid":"`+state.DeviceUUID+`","status":"approved","numeric_id":2,"slot_id":"`+slotID+`","sim_id":"`+simID+`","slot_key":"primary","slot_index":1}}`), nil
		})

		result, err := client.SubmitDiscovery(context.Background(), credential, state)

		Expect(err).NotTo(HaveOccurred())
		Expect(result.SlotID).To(Equal(slotID))
		Expect(result.SIMID).To(Equal(simID))
	})

	It("polls an approved discovery with the pending credential", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/v1/device-discoveries/" + state.DeviceUUID + "/status"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer " + credential))
			return response(http.StatusOK, `{"discovery":{"device_uuid":"`+state.DeviceUUID+`","status":"approved","numeric_id":2}}`), nil
		})

		result, err := client.GetDiscoveryStatus(context.Background(), state.DeviceUUID, credential)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(DiscoveryResult{DeviceUUID: state.DeviceUUID, Status: DiscoveryApproved, NumericID: 2}))
	})

	It("maps unavailable status without echoing the credential", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32))
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_pending_credential","message":"secret details"}}`), nil
		})

		_, err := client.GetDiscoveryStatus(context.Background(), state.DeviceUUID, credential)

		Expect(errors.Is(err, ErrDiscoveryUnavailable)).To(BeTrue())
		Expect(err.Error()).NotTo(ContainSubstring(credential))
	})

	It("returns bounded submission errors without echoing the pending credential", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusConflict, `{"error":{"code":"device_discovery_closed","message":"secret details"}}`), nil
		})

		_, err := client.SubmitDiscovery(context.Background(), credential, state)

		Expect(err).To(MatchError("submit device discovery: server returned 409 (device_discovery_closed)"))
		Expect(err.Error()).NotTo(ContainSubstring(credential))
		Expect(err.Error()).NotTo(ContainSubstring("secret details"))
	})

	DescribeTable("rejects invalid discovery results",
		func(payload string) {
			client := clientWithHandler(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, payload), nil
			})
			_, err := client.GetDiscoveryStatus(context.Background(), state.DeviceUUID, "credential")
			Expect(err).To(MatchError(ContainSubstring("invalid device discovery")))
		},
		Entry("different device", `{"discovery":{"device_uuid":"01900000-0000-7000-8000-000000000099","status":"pending"}}`),
		Entry("unknown state", `{"discovery":{"device_uuid":"01900000-0000-7000-8000-000000000001","status":"unknown"}}`),
		Entry("pending numeric id", `{"discovery":{"device_uuid":"01900000-0000-7000-8000-000000000001","status":"pending","numeric_id":2}}`),
		Entry("approved missing id", `{"discovery":{"device_uuid":"01900000-0000-7000-8000-000000000001","status":"approved"}}`),
		Entry("approved high id", `{"discovery":{"device_uuid":"01900000-0000-7000-8000-000000000001","status":"approved","numeric_id":100}}`),
	)

	DescribeTable("rejects malformed approved slot identities",
		func(slotID string, simID string) {
			client := clientWithHandler(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, `{"discovery":{"device_uuid":"`+state.DeviceUUID+`","status":"approved","numeric_id":2,"slot_id":"`+slotID+`","sim_id":"`+simID+`"}}`), nil
			})

			_, err := client.GetDiscoveryStatus(context.Background(), state.DeviceUUID, "credential")

			Expect(err).To(MatchError("server returned invalid slot or SIM identity"))
		},
		Entry("slot ID", "invalid", "01900000-0000-7000-8000-000000000003"),
		Entry("SIM ID", "01900000-0000-7000-8000-000000000002", "invalid"),
	)

	It("rejects missing discovery inputs before making a request", func() {
		_, err := NewClient("http://unused.invalid", nil).SubmitDiscovery(context.Background(), "", state)
		Expect(err).To(MatchError("pending credential and valid device identity are required"))
		_, err = NewClient("http://unused.invalid", nil).GetDiscoveryStatus(context.Background(), "", "credential")
		Expect(err).To(MatchError("device UUID and pending credential are required"))
		state.IdentityPrivateKey = nil
		_, err = NewClient("http://unused.invalid", nil).SubmitDiscovery(context.Background(), "credential", state)
		Expect(err).To(MatchError("pending credential and valid device identity are required"))
	})

	It("reports malformed discovery JSON and transport failures", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, "not-json"), nil
		})
		_, err := client.GetDiscoveryStatus(context.Background(), state.DeviceUUID, "credential")
		Expect(err).To(MatchError(ContainSubstring("decode device discovery response")))

		_, err = NewClient("://invalid", nil).GetDiscoveryStatus(context.Background(), state.DeviceUUID, "credential")
		Expect(err).To(MatchError(ContainSubstring("create discovery status request")))
		client = clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		_, err = client.SubmitDiscovery(context.Background(), "credential", state)
		Expect(err).To(MatchError(ContainSubstring("submit device discovery")))
	})

	It("registers the current command public key", func() {
		state, err := identity.NewStore(GinkgoT().TempDir(), "01900000-0000-7000-8000-000000000001").LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		state.Credential = "device-secret"
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPut))
			Expect(request.URL.Path).To(Equal("/v1/device-command-key"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			var body struct {
				DeviceUUID       string `json:"device_uuid"`
				CommandPublicKey []byte `json:"command_public_key"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.CommandPublicKey).To(Equal(state.CommandPublicKey()))
			return response(http.StatusNoContent, ""), nil
		})

		Expect(client.UpdateCommandKey(context.Background(), state)).To(Succeed())
	})

	It("maps command key request, transport, and server errors", func() {
		state, err := identity.NewStore(GinkgoT().TempDir(), "01900000-0000-7000-8000-000000000001").LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
		Expect(NewClient("://invalid", nil).UpdateCommandKey(context.Background(), state)).To(MatchError(ContainSubstring("create command key update request")))

		transportFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		Expect(transportFailure.UpdateCommandKey(context.Background(), state)).To(MatchError(ContainSubstring("update device command key")))

		serverFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_device_credential"}}`), nil
		})
		Expect(serverFailure.UpdateCommandKey(context.Background(), state)).To(MatchError(ContainSubstring("invalid_device_credential")))
	})
})

var _ = Describe("Encrypted command client", func() {
	const (
		deviceID  = "01900000-0000-7000-8000-000000000001"
		commandID = "01900000-0000-7000-8000-000000000010"
	)

	It("claims and decodes an opaque command", func() {
		encrypted := command.Envelope{
			ID: commandID, DeviceUUID: deviceID, SenderPublicKey: bytes.Repeat([]byte{1}, 32),
			Nonce: bytes.Repeat([]byte{2}, 12), Ciphertext: []byte("sealed"),
			CreatedAt: time.Unix(1_000, 0).UTC(), ExpiresAt: time.Unix(1_060, 0).UTC(),
		}
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodGet))
			Expect(request.URL.Path).To(Equal("/v1/device-commands/next"))
			Expect(request.URL.Query().Get("device_uuid")).To(Equal(deviceID))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			body, err := json.Marshal(map[string]any{"command": encrypted})
			Expect(err).NotTo(HaveOccurred())
			return response(http.StatusOK, string(body)), nil
		})

		claimed, err := client.Claim(context.Background(), deviceID, "device-secret")

		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(Equal(&encrypted))
	})

	It("returns nil for an empty command queue", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusNoContent, ""), nil
		})
		claimed, err := client.Claim(context.Background(), deviceID, "device-secret")
		Expect(err).NotTo(HaveOccurred())
		Expect(claimed).To(BeNil())
	})

	It("completes a command with only encrypted result bytes", func() {
		result := command.EncryptedResult{Nonce: bytes.Repeat([]byte{3}, 12), Ciphertext: []byte("opaque-result")}
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.Path).To(Equal("/v1/device-commands/" + commandID + "/complete"))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			Expect(request.Header.Get("Content-Type")).To(Equal("application/json"))
			var body struct {
				DeviceUUID string                  `json:"device_uuid"`
				Result     command.EncryptedResult `json:"result"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.DeviceUUID).To(Equal(deviceID))
			Expect(body.Result).To(Equal(result))
			return response(http.StatusNoContent, ""), nil
		})

		Expect(client.Complete(context.Background(), deviceID, "device-secret", commandID, result)).To(Succeed())
	})

	DescribeTable("maps server command errors without exposing response details",
		func(operation string, status int, body string, expected string) {
			client := clientWithHandler(func(*http.Request) (*http.Response, error) {
				return response(status, body), nil
			})
			var err error
			if operation == "claim" {
				_, err = client.Claim(context.Background(), deviceID, "device-secret")
			} else {
				err = client.Complete(context.Background(), deviceID, "device-secret", commandID, command.EncryptedResult{})
			}
			Expect(err).To(MatchError(ContainSubstring(expected)))
			Expect(err.Error()).NotTo(ContainSubstring("sensitive detail"))
		},
		Entry("claim JSON error", "claim", http.StatusUnauthorized, `{"error":{"code":"invalid_device_credential","message":"sensitive detail"}}`, "invalid_device_credential"),
		Entry("claim non-JSON error", "claim", http.StatusBadGateway, "sensitive detail", "unexpected_response"),
		Entry("complete JSON error", "complete", http.StatusNotFound, `{"error":{"code":"command_not_found","message":"sensitive detail"}}`, "command_not_found"),
	)

	It("reports malformed claim responses", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, "not-json"), nil
		})
		_, err := client.Claim(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("decode encrypted command")))
	})

	It("reports request construction and transport failures", func() {
		invalid := NewClient("://invalid", nil)
		_, err := invalid.Claim(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("create command claim request")))
		Expect(invalid.Complete(context.Background(), deviceID, "device-secret", commandID, command.EncryptedResult{})).To(MatchError(ContainSubstring("create command completion request")))

		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		_, err = client.Claim(context.Background(), deviceID, "device-secret")
		Expect(err).To(MatchError(ContainSubstring("claim encrypted command")))
		Expect(client.Complete(context.Background(), deviceID, "device-secret", commandID, command.EncryptedResult{})).To(MatchError(ContainSubstring("complete encrypted command")))
	})
})

var _ = Describe("Encrypted event client", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"

	It("uploads an opaque event with device authentication", func() {
		var received callevent.Envelope
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.URL.Path).To(Equal("/v1/events"))
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.Header.Get("Authorization")).To(Equal("Bearer device-secret"))
			var body struct {
				Envelope callevent.Envelope `json:"envelope"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			received = body.Envelope
			return response(http.StatusCreated, `{}`), nil
		})
		envelope := callevent.Envelope{ID: "event-1", DeviceUUID: deviceID, Ciphertext: []byte("opaque")}

		Expect(client.UploadEvent(context.Background(), deviceID, "device-secret", envelope)).To(Succeed())
		Expect(received).To(Equal(envelope))
	})

	It("accepts an idempotent retry and maps invalid requests", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusOK, `{}`), nil
		})
		envelope := callevent.Envelope{DeviceUUID: deviceID}
		Expect(client.UploadEvent(context.Background(), deviceID, "device-secret", envelope)).To(Succeed())
		Expect(client.UploadEvent(context.Background(), "another-device", "device-secret", envelope)).To(MatchError(ContainSubstring("does not match")))
	})

	It("maps event transport and server failures", func() {
		envelope := callevent.Envelope{DeviceUUID: deviceID}
		transportFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})
		Expect(transportFailure.UploadEvent(context.Background(), deviceID, "device-secret", envelope)).To(MatchError(ContainSubstring("upload encrypted event")))

		serverFailure := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_device_credential"}}`), nil
		})
		Expect(serverFailure.UploadEvent(context.Background(), deviceID, "device-secret", envelope)).To(MatchError(ContainSubstring("invalid_device_credential")))
	})
})
