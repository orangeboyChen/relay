package serverapi

import (
	"bytes"
	"context"
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

var _ = Describe("Installation recipient client", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"

	It("lists approved public encryption identities with device authentication", func() {
		recipients := []InstallationRecipient{{
			InstallationID:        "01900000-0000-7000-8000-000000000101",
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
		func(installationID string, publicKey []byte) {
			body, err := json.Marshal(map[string]any{"installations": []InstallationRecipient{{
				InstallationID: installationID, KeyAgreementPublicKey: publicKey,
			}}})
			Expect(err).NotTo(HaveOccurred())
			client := clientWithHandler(func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, string(body)), nil
			})

			_, err = client.ListInstallationRecipients(context.Background(), deviceID, "device-secret")

			Expect(err).To(MatchError("server returned an invalid installation recipient"))
		},
		Entry("invalid installation UUID", "not-a-uuid", bytes.Repeat([]byte{8}, 32)),
		Entry("invalid X25519 key", "01900000-0000-7000-8000-000000000101", bytes.Repeat([]byte{8}, 31)),
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

var _ = Describe("Server pairing client", func() {
	var state identity.State

	BeforeEach(func() {
		var err error
		state, err = identity.NewStore(GinkgoT().TempDir()).LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
	})

	It("registers only the public identity and accepts the returned binding", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
		client := clientWithHandler(func(request *http.Request) (*http.Response, error) {
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.Path).To(Equal("/v1/device-pairings"))
			var body struct {
				PairingToken      string `json:"pairing_token"`
				DeviceUUID        string `json:"device_uuid"`
				IdentityPublicKey []byte `json:"identity_public_key"`
				CommandPublicKey  []byte `json:"command_public_key"`
				ProfileCiphertext []byte `json:"profile_ciphertext"`
				DeviceCredential  string `json:"device_credential"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.PairingToken).To(Equal("one-time-token"))
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.IdentityPublicKey).To(Equal([]byte(state.PublicKey())))
			Expect(body.IdentityPublicKey).NotTo(Equal([]byte(state.IdentityPrivateKey)))
			Expect(body.CommandPublicKey).To(Equal(state.CommandPublicKey()))
			Expect(body.ProfileCiphertext).To(BeEmpty())
			Expect(body.DeviceCredential).To(Equal(credential))
			return response(http.StatusCreated, `{"device":{"device_uuid":"`+state.DeviceUUID+`","numeric_id":2}}`), nil
		})

		result, err := client.Pair(context.Background(), "one-time-token", credential, state)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(PairingResult{NumericID: 2}))
	})

	It("returns a bounded server error without echoing the pairing secret", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnauthorized, `{"error":{"code":"invalid_pairing_token","message":"secret details"}}`), nil
		})

		_, err := client.Pair(context.Background(), "one-time-token", "device-credential", state)

		Expect(err).To(MatchError("pair device: server returned 401 (invalid_pairing_token)"))
		Expect(err.Error()).NotTo(ContainSubstring("one-time-token"))
		Expect(err.Error()).NotTo(ContainSubstring("secret details"))
	})

	It("uses a generic code when the server error is not JSON", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusBadGateway, "upstream failed"), nil
		})

		_, err := client.Pair(context.Background(), "token", "device-credential", state)

		Expect(err).To(MatchError("pair device: server returned 502 (unexpected_response)"))
	})

	DescribeTable("rejects invalid bindings",
		func(deviceUUID string, numericID int) {
			client := clientWithHandler(func(*http.Request) (*http.Response, error) {
				body, err := json.Marshal(map[string]any{
					"device": map[string]any{"device_uuid": deviceUUID, "numeric_id": numericID},
				})
				Expect(err).NotTo(HaveOccurred())
				return response(http.StatusCreated, string(body)), nil
			})

			_, err := client.Pair(context.Background(), "one-time-token", "device-credential", state)

			Expect(err).To(MatchError("pair device: server returned an invalid binding"))
		},
		Entry("different device", "01900000-0000-7000-8000-000000000099", 1),
		Entry("numeric ID below range", "", 0),
		Entry("numeric ID above range", "", 100),
	)

	It("rejects missing inputs before making a request", func() {
		_, err := NewClient("http://unused.invalid", nil).Pair(context.Background(), "", "credential", state)
		Expect(err).To(MatchError("pairing token, device credential, and valid device identity are required"))

		_, err = NewClient("http://unused.invalid", nil).Pair(context.Background(), "token", "", state)
		Expect(err).To(MatchError("pairing token, device credential, and valid device identity are required"))

		state.IdentityPrivateKey = nil
		_, err = NewClient("http://unused.invalid", nil).Pair(context.Background(), "token", "credential", state)
		Expect(err).To(MatchError("pairing token, device credential, and valid device identity are required"))
	})

	It("reports malformed success JSON", func() {
		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return response(http.StatusCreated, "not-json"), nil
		})

		_, err := client.Pair(context.Background(), "token", "device-credential", state)

		Expect(err).To(MatchError(ContainSubstring("decode pairing response")))
	})

	It("reports request construction and transport failures", func() {
		_, err := NewClient("://invalid", nil).Pair(context.Background(), "token", "device-credential", state)
		Expect(err).To(MatchError(ContainSubstring("create pairing request")))

		client := clientWithHandler(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport unavailable")
		})
		_, err = client.Pair(context.Background(), "token", "device-credential", state)
		Expect(err).To(MatchError(ContainSubstring("pair device")))
	})

	It("registers the current command public key", func() {
		state, err := identity.NewStore(GinkgoT().TempDir()).LoadOrCreate()
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
		state, err := identity.NewStore(GinkgoT().TempDir()).LoadOrCreate()
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
			CreatedAt: time.Unix(1_000, 0), ExpiresAt: time.Unix(1_060, 0),
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
