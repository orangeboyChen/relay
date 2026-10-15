package serverapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/identity"
)

var _ = Describe("Server pairing client", func() {
	var state identity.State

	BeforeEach(func() {
		var err error
		state, err = identity.NewStore(GinkgoT().TempDir()).LoadOrCreate()
		Expect(err).NotTo(HaveOccurred())
	})

	It("registers only the public identity and accepts the returned binding", func() {
		credential := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.Path).To(Equal("/v1/device-pairings"))
			var body struct {
				PairingToken      string `json:"pairing_token"`
				DeviceUUID        string `json:"device_uuid"`
				IdentityPublicKey []byte `json:"identity_public_key"`
				ProfileCiphertext []byte `json:"profile_ciphertext"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.PairingToken).To(Equal("one-time-token"))
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.IdentityPublicKey).To(Equal([]byte(state.PublicKey())))
			Expect(body.IdentityPublicKey).NotTo(Equal([]byte(state.IdentityPrivateKey)))
			Expect(body.ProfileCiphertext).To(BeEmpty())
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			Expect(json.NewEncoder(writer).Encode(map[string]any{
				"device":     map[string]any{"device_uuid": state.DeviceUUID, "numeric_id": 2},
				"credential": credential,
			})).To(Succeed())
		}))
		defer server.Close()

		result, err := NewClient(server.URL, server.Client()).Pair(context.Background(), "one-time-token", state)

		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(PairingResult{Credential: credential, NumericID: 2}))
	})

	It("returns a bounded server error without echoing the pairing secret", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":{"code":"invalid_pairing_token","message":"secret details"}}`))
		}))
		defer server.Close()

		_, err := NewClient(server.URL, server.Client()).Pair(context.Background(), "one-time-token", state)

		Expect(err).To(MatchError("pair device: server returned 401 (invalid_pairing_token)"))
		Expect(err.Error()).NotTo(ContainSubstring("one-time-token"))
		Expect(err.Error()).NotTo(ContainSubstring("secret details"))
	})

	It("uses a generic code when the server error is not JSON", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte("upstream failed"))
		}))
		defer server.Close()

		_, err := NewClient(server.URL, server.Client()).Pair(context.Background(), "token", state)

		Expect(err).To(MatchError("pair device: server returned 502 (unexpected_response)"))
	})

	DescribeTable("rejects invalid bindings",
		func(deviceUUID string, numericID int, credential string) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(writer).Encode(map[string]any{
					"device":     map[string]any{"device_uuid": deviceUUID, "numeric_id": numericID},
					"credential": credential,
				})
			}))
			defer server.Close()

			_, err := NewClient(server.URL, server.Client()).Pair(context.Background(), "one-time-token", state)

			Expect(err).To(MatchError("pair device: server returned an invalid binding"))
		},
		Entry("different device", "01900000-0000-7000-8000-000000000099", 1, "credential"),
		Entry("numeric ID below range", "", 0, "credential"),
		Entry("numeric ID above range", "", 100, "credential"),
		Entry("empty credential", "", 1, ""),
	)

	It("rejects missing inputs before making a request", func() {
		_, err := NewClient("http://unused.invalid", nil).Pair(context.Background(), "", state)
		Expect(err).To(MatchError("pairing token and valid device identity are required"))

		state.IdentityPrivateKey = nil
		_, err = NewClient("http://unused.invalid", nil).Pair(context.Background(), "token", state)
		Expect(err).To(MatchError("pairing token and valid device identity are required"))
	})

	It("reports malformed success JSON", func() {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte("not-json"))
		}))
		defer server.Close()

		_, err := NewClient(server.URL, server.Client()).Pair(context.Background(), "token", state)

		Expect(err).To(MatchError(ContainSubstring("decode pairing response")))
	})

	It("reports request construction and transport failures", func() {
		_, err := NewClient("://invalid", nil).Pair(context.Background(), "token", state)
		Expect(err).To(MatchError(ContainSubstring("create pairing request")))

		context, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = NewClient("https://deck.invalid", nil).Pair(context, "token", state)
		Expect(err).To(MatchError(ContainSubstring("pair device")))
	})
})
