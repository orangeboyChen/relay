//go:build legacy_rest_tests

package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Incoming call claim API", func() {
	const (
		notificationID = "01900000-0000-7000-8000-000000000010"
		installationID = "01900000-0000-7000-8000-000000000101"
	)
	var (
		now        time.Time
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
		repository = &fakeRepository{
			ownerID: "owner-1",
			incomingClaim: domain.IncomingCallClaim{
				NotificationID: notificationID,
				InstallationID: installationID,
				ClaimedAt:      now,
				Won:            true,
			},
		}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	request := func(path string, body string, authenticated bool) *http.Request {
		result := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
		if authenticated {
			result.Header.Set("Authorization", "Bearer owner-access-token")
		}
		return result
	}

	It("atomically claims for the authenticated approved installation", func() {
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request(
			"/v1/incoming-calls/"+notificationID+"/claim",
			`{"installation_id":"`+installationID+`"}`,
			true,
		))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(repository.incomingClaimOwnerID).To(Equal("owner-1"))
		Expect(repository.incomingClaimNotificationID).To(Equal(notificationID))
		Expect(repository.incomingClaimInstallationID).To(Equal(installationID))
		Expect(repository.incomingClaimNow).To(Equal(now))
		Expect(response.Body.String()).To(ContainSubstring(`"won":true`))
	})

	It("returns the existing winner to later iPhones", func() {
		repository.incomingClaim.InstallationID = "01900000-0000-7000-8000-000000000102"
		repository.incomingClaim.Won = false
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request(
			"/v1/incoming-calls/"+notificationID+"/claim",
			`{"installation_id":"`+installationID+`"}`,
			true,
		))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"won":false`))
		Expect(response.Body.String()).To(ContainSubstring(repository.incomingClaim.InstallationID))
	})

	It("requires owner authentication", func() {
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request(
			"/v1/incoming-calls/"+notificationID+"/claim",
			`{"installation_id":"`+installationID+`"}`,
			false,
		))

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.incomingClaimNotificationID).To(BeEmpty())
	})

	DescribeTable("rejects malformed claims before storage",
		func(path string, body string, expectedStatus int) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request(path, body, true))
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(repository.incomingClaimNotificationID).To(BeEmpty())
		},
		Entry("malformed JSON", "/v1/incoming-calls/"+notificationID+"/claim", `{`, http.StatusBadRequest),
		Entry("invalid notification", "/v1/incoming-calls/invalid/claim", `{"installation_id":"`+installationID+`"}`, http.StatusUnprocessableEntity),
		Entry("invalid installation", "/v1/incoming-calls/"+notificationID+"/claim", `{"installation_id":"invalid"}`, http.StatusUnprocessableEntity),
	)

	DescribeTable("maps claim failures without leaking storage details",
		func(repositoryError error, expectedStatus int, expectedCode string) {
			repository.incomingClaimError = repositoryError
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request(
				"/v1/incoming-calls/"+notificationID+"/claim",
				`{"installation_id":"`+installationID+`"}`,
				true,
			))
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
			Expect(response.Body.String()).NotTo(ContainSubstring("database unavailable"))
		},
		Entry("unavailable call", pgx.ErrNoRows, http.StatusNotFound, "incoming_call_unavailable"),
		Entry("database failure", errors.New("database unavailable"), http.StatusInternalServerError, "storage_error"),
	)
})
