//go:build legacy_rest_tests

package api

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Device radio status", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	now := time.Date(2026, time.October, 16, 8, 0, 0, 0, time.UTC)

	requestStatus := func(repository *fakeRepository, body string, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/v1/device-radio-status", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		Handler(repository, Config{Now: func() time.Time { return now }}).ServeHTTP(response, request)
		return response
	}

	It("authenticates and stores current LTE signal bars", func() {
		repository := &fakeRepository{}
		response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","slot_key":"primary","slot_index":2,"radio_access_technology":"lte","signal_bars":3}`, "device-secret")

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.authenticatedDeviceID).To(Equal(deviceID))
		Expect(repository.authenticatedCredential).To(Equal("device-secret"))
		Expect(repository.updatedRadioDevice).To(Equal(deviceID))
		Expect(repository.updatedRadioSlotKey).To(Equal("primary"))
		Expect(repository.updatedRadioSlotIndex).To(Equal(2))
		Expect(repository.updatedRadioTechnology).To(Equal("lte"))
		Expect(repository.updatedSignalBars).NotTo(BeNil())
		Expect(*repository.updatedSignalBars).To(Equal(3))
		Expect(repository.updatedRadioAt).To(Equal(now))
	})

	It("accepts an unknown signal strength without inventing bars", func() {
		repository := &fakeRepository{}
		response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","radio_access_technology":"unknown","signal_bars":null}`, "device-secret")

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.updatedSignalBars).To(BeNil())
		Expect(repository.updatedRadioSlotKey).To(Equal("1"))
		Expect(repository.updatedRadioSlotIndex).To(Equal(1))
	})

	It("rejects malformed slot identities before authentication or storage", func() {
		repository := &fakeRepository{}
		response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","slot_key":"primary","slot_index":33,"radio_access_technology":"lte","signal_bars":3}`, "device-secret")

		Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(response.Body.String()).To(ContainSubstring("invalid_slot_identity"))
		Expect(repository.authenticatedDeviceID).To(BeEmpty())
		Expect(repository.updatedRadioDevice).To(BeEmpty())
	})

	It("rejects invalid radio values before storage", func() {
		repository := &fakeRepository{}
		response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","radio_access_technology":"satellite","signal_bars":5}`, "device-secret")

		Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(repository.updatedRadioDevice).To(BeEmpty())
	})

	It("does not store status when device authentication fails", func() {
		repository := &fakeRepository{deviceAuthError: errors.New("denied")}
		response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","radio_access_technology":"lte","signal_bars":4}`, "wrong")

		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.updatedRadioDevice).To(BeEmpty())
	})

	It("rejects malformed requests and missing device credentials", func() {
		repository := &fakeRepository{}
		Expect(requestStatus(repository, `{`, "device-secret").Code).To(Equal(http.StatusBadRequest))
		Expect(requestStatus(repository, `{"device_uuid":"`+deviceID+`","radio_access_technology":"lte"}`, "").Code).To(Equal(http.StatusUnauthorized))
		Expect(requestStatus(repository, `{"device_uuid":"not-a-uuid","radio_access_technology":"lte"}`, "device-secret").Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.updatedRadioDevice).To(BeEmpty())
	})

	DescribeTable("maps radio status storage failures",
		func(storageError error, expectedStatus int, expectedCode string) {
			repository := &fakeRepository{updateRadioError: storageError}
			response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","radio_access_technology":"lte","signal_bars":2}`, "device-secret")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("missing device", pgx.ErrNoRows, http.StatusNotFound, "device_not_found"),
		Entry("database failure", errors.New("database offline"), http.StatusInternalServerError, "storage_error"),
	)
})

var _ = Describe("Device SIM identity", func() {
	const deviceID = "01900000-0000-7000-8000-000000000001"
	now := time.Date(2026, time.October, 16, 8, 0, 0, 0, time.UTC)
	validFingerprint := strings.Repeat("ab", 32)

	requestIdentity := func(repository *fakeRepository, body string, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPut, "/v1/device-sim-identity", bytes.NewBufferString(body))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		Handler(repository, Config{Now: func() time.Time { return now }}).ServeHTTP(response, request)
		return response
	}

	validBody := func() string {
		return `{"device_uuid":"` + deviceID + `","slot_key":"primary","slot_index":1,"sim_fingerprint":"` + validFingerprint + `"}`
	}

	It("authenticates and assigns the SIM to its physical slot", func() {
		repository := &fakeRepository{
			updatedSIMID:        "01900000-0000-7000-8000-000000000099",
			updatedSIMNumericID: 7,
		}

		response := requestIdentity(repository, validBody(), "device-secret")

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(MatchJSON(`{"slot_id":"01900000-0000-7000-8000-000000000098","numeric_id":7,"sim_id":"01900000-0000-7000-8000-000000000099"}`))
		Expect(repository.authenticatedDeviceID).To(Equal(deviceID))
		Expect(repository.authenticatedCredential).To(Equal("device-secret"))
		Expect(repository.updatedSIMDevice).To(Equal(deviceID))
		Expect(repository.updatedSIMSlotKey).To(Equal("primary"))
		Expect(repository.updatedSIMSlotIndex).To(Equal(1))
		Expect(repository.updatedSIMFingerprint).To(Equal(bytes.Repeat([]byte{0xab}, 32)))
		Expect(repository.updatedSIMAt).To(Equal(now))
	})

	It("rejects malformed JSON and missing device credentials", func() {
		repository := &fakeRepository{}
		Expect(requestIdentity(repository, `{`, "device-secret").Code).To(Equal(http.StatusBadRequest))
		Expect(requestIdentity(repository, validBody(), "").Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.updatedSIMDevice).To(BeEmpty())
	})

	DescribeTable("rejects invalid physical slot identities",
		func(slotKey string, slotIndex int) {
			repository := &fakeRepository{}
			body := `{"device_uuid":"` + deviceID + `","slot_key":"` + slotKey + `","slot_index":` +
				fmt.Sprintf("%d", slotIndex) + `,"sim_fingerprint":"` + validFingerprint + `"}`
			response := requestIdentity(repository, body, "device-secret")
			Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
			Expect(repository.authenticatedDeviceID).To(BeEmpty())
		},
		Entry("empty key", "", 1),
		Entry("oversized key", strings.Repeat("x", 65), 1),
		Entry("zero index", "primary", 0),
		Entry("oversized index", "primary", 33),
	)

	It("rejects malformed fingerprints before authentication or storage", func() {
		repository := &fakeRepository{}
		response := requestIdentity(repository, `{"device_uuid":"`+deviceID+`","slot_key":"1","slot_index":1,"sim_fingerprint":"bad"}`, "device-secret")
		Expect(response.Code).To(Equal(http.StatusUnprocessableEntity))
		Expect(repository.authenticatedDeviceID).To(BeEmpty())
	})

	It("does not mutate slot identity when device authentication fails", func() {
		repository := &fakeRepository{deviceAuthError: errors.New("denied")}
		response := requestIdentity(repository, validBody(), "wrong")
		Expect(response.Code).To(Equal(http.StatusUnauthorized))
		Expect(repository.updatedSIMDevice).To(BeEmpty())
	})

	DescribeTable("maps storage errors without exposing internals",
		func(storageError error, expectedStatus int, expectedCode string) {
			repository := &fakeRepository{updateSIMError: storageError}
			response := requestIdentity(repository, validBody(), "device-secret")
			Expect(response.Code).To(Equal(expectedStatus))
			Expect(response.Body.String()).To(ContainSubstring(expectedCode))
		},
		Entry("missing slot", pgx.ErrNoRows, http.StatusNotFound, "device_slot_not_found"),
		Entry("exhausted IDs", domain.ErrNoNumericID, http.StatusConflict, "numeric_ids_exhausted"),
		Entry("database failure", errors.New("database offline"), http.StatusInternalServerError, "storage_error"),
	)
})
