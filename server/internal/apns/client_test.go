package apns

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("APNs client", func() {
	var (
		privateKey *ecdsa.PrivateKey
		privatePEM []byte
		now        time.Time
	)

	BeforeEach(func() {
		var err error
		privateKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		Expect(err).NotTo(HaveOccurred())
		encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
		Expect(err).NotTo(HaveOccurred())
		privatePEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
		now = time.Date(2026, 10, 16, 4, 0, 0, 0, time.UTC)
	})

	newClient := func(server *httptest.Server) *Client {
		client, err := New(Config{
			KeyID:         "KEY123",
			TeamID:        "TEAM123",
			Topic:         "com.nowcent.simdeck.voip",
			PrivateKeyPEM: privatePEM,
			HTTPClient:    server.Client(),
			ProductionURL: server.URL,
			SandboxURL:    server.URL,
			Now:           func() time.Time { return now },
		})
		Expect(err).NotTo(HaveOccurred())
		return client
	}

	It("sends an immediate encrypted VoIP payload with a valid provider token", func() {
		requests := 0
		issuedAt := now
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			defer GinkgoRecover()
			requests++
			Expect(request.Method).To(Equal(http.MethodPost))
			Expect(request.URL.Path).To(Equal("/3/device/0001feff"))
			Expect(request.Header.Get("apns-topic")).To(Equal("com.nowcent.simdeck.voip"))
			Expect(request.Header.Get("apns-push-type")).To(Equal("voip"))
			Expect(request.Header.Get("apns-priority")).To(Equal("10"))
			Expect(request.Header.Get("apns-expiration")).To(Equal("0"))
			verifyProviderToken(strings.TrimPrefix(request.Header.Get("authorization"), "bearer "), &privateKey.PublicKey, issuedAt)

			body, err := io.ReadAll(request.Body)
			Expect(err).NotTo(HaveOccurred())
			var payload map[string]json.RawMessage
			Expect(json.Unmarshal(body, &payload)).To(Succeed())
			Expect(payload).To(HaveKey("ciphertext"))
			Expect(payload).To(HaveKey("aps"))
			Expect(string(payload["aps"])).To(MatchJSON(`{"content-available":1}`))
			writer.WriteHeader(http.StatusOK)
		}))
		defer server.Close()
		client := newClient(server)

		Expect(client.SendVoIP(
			context.Background(),
			"sandbox",
			[]byte{0, 1, 254, 255},
			[]byte(`{"notification_id":"opaque","ciphertext":"encrypted"}`),
		)).To(Succeed())
		now = now.Add(10 * time.Minute)
		Expect(client.SendVoIP(
			context.Background(),
			"production",
			[]byte{0, 1, 254, 255},
			[]byte(`{"notification_id":"opaque","ciphertext":"encrypted"}`),
		)).To(Succeed())
		Expect(requests).To(Equal(2))
	})

	DescribeTable("classifies APNs failures",
		func(status int, reason string, retryable bool, invalidates bool) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(`{"reason":"` + reason + `"}`))
			}))
			defer server.Close()
			client := newClient(server)

			err := client.SendVoIP(context.Background(), "production", []byte{1}, []byte(`{"ciphertext":"opaque"}`))

			var responseError *ResponseError
			Expect(errors.As(err, &responseError)).To(BeTrue())
			Expect(responseError.Error()).To(ContainSubstring(reason))
			Expect(responseError.Retryable()).To(Equal(retryable))
			Expect(responseError.InvalidatesToken()).To(Equal(invalidates))
		},
		Entry("expired token", http.StatusGone, "Unregistered", false, true),
		Entry("bad token", http.StatusBadRequest, "BadDeviceToken", false, true),
		Entry("wrong topic", http.StatusBadRequest, "DeviceTokenNotForTopic", false, true),
		Entry("throttled", http.StatusTooManyRequests, "TooManyRequests", true, false),
		Entry("unavailable", http.StatusServiceUnavailable, "ServiceUnavailable", true, false),
		Entry("other client error", http.StatusBadRequest, "PayloadEmpty", false, false),
	)

	It("formats an APNs failure even when the service omits a reason", func() {
		failure := &ResponseError{StatusCode: http.StatusForbidden}

		Expect(failure.Error()).To(Equal("APNs returned HTTP 403"))
		Expect(failure.Retryable()).To(BeFalse())
		Expect(failure.InvalidatesToken()).To(BeFalse())
	})

	It("rejects invalid configuration and payloads", func() {
		_, err := New(Config{})
		Expect(err).To(MatchError(ErrInvalidConfiguration))
		_, err = New(Config{KeyID: "key", TeamID: "team", Topic: "topic", PrivateKeyPEM: []byte("invalid")})
		Expect(err).To(MatchError(ErrInvalidConfiguration))

		server := httptest.NewServer(http.NotFoundHandler())
		defer server.Close()
		client := newClient(server)
		Expect(client.SendVoIP(context.Background(), "invalid", []byte{1}, []byte(`{}`))).To(MatchError(ErrInvalidConfiguration))
		Expect(client.SendVoIP(context.Background(), "sandbox", nil, []byte(`{}`))).To(MatchError(ErrInvalidConfiguration))
		Expect(client.SendVoIP(context.Background(), "sandbox", []byte{1}, []byte(`not-json`))).To(MatchError(ErrInvalidPayload))
		Expect(client.SendVoIP(context.Background(), "sandbox", []byte{1}, []byte(`{"aps":{}}`))).To(MatchError(ErrInvalidPayload))
		large := []byte(`{"ciphertext":"` + strings.Repeat("a", maximumPayloadSize) + `"}`)
		Expect(client.SendVoIP(context.Background(), "sandbox", []byte{1}, large)).To(MatchError(ErrInvalidPayload))
	})

	It("returns transport failures without converting them to APNs responses", func() {
		client, err := New(Config{
			KeyID:         "KEY123",
			TeamID:        "TEAM123",
			Topic:         "com.nowcent.simdeck.voip",
			PrivateKeyPEM: privatePEM,
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("network unavailable")
			})},
			Now: func() time.Time { return now },
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(client.SendVoIP(context.Background(), "sandbox", []byte{1}, []byte(`{}`))).To(MatchError(ContainSubstring("network unavailable")))
	})
})

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func verifyProviderToken(token string, publicKey *ecdsa.PublicKey, issuedAt time.Time) {
	parts := strings.Split(token, ".")
	Expect(parts).To(HaveLen(3))
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	Expect(err).NotTo(HaveOccurred())
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	Expect(err).NotTo(HaveOccurred())
	var header map[string]any
	var claims map[string]any
	Expect(json.Unmarshal(headerBytes, &header)).To(Succeed())
	Expect(json.Unmarshal(claimsBytes, &claims)).To(Succeed())
	Expect(header).To(HaveKeyWithValue("alg", "ES256"))
	Expect(header).To(HaveKeyWithValue("kid", "KEY123"))
	Expect(claims).To(HaveKeyWithValue("iss", "TEAM123"))
	Expect(claims["iat"]).To(BeNumerically("==", issuedAt.Unix()))

	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	Expect(err).NotTo(HaveOccurred())
	Expect(signature).To(HaveLen(64))
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	Expect(ecdsa.Verify(publicKey, digest[:], newBigInt(signature[:32]), newBigInt(signature[32:]))).To(BeTrue())
}

func newBigInt(encoded []byte) *big.Int {
	return new(big.Int).SetBytes(encoded)
}
