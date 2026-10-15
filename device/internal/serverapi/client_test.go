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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
				ProfileCiphertext []byte `json:"profile_ciphertext"`
				DeviceCredential  string `json:"device_credential"`
			}
			Expect(json.NewDecoder(request.Body).Decode(&body)).To(Succeed())
			Expect(body.PairingToken).To(Equal("one-time-token"))
			Expect(body.DeviceUUID).To(Equal(state.DeviceUUID))
			Expect(body.IdentityPublicKey).To(Equal([]byte(state.PublicKey())))
			Expect(body.IdentityPublicKey).NotTo(Equal([]byte(state.IdentityPrivateKey)))
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
})
