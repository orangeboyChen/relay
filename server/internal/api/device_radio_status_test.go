package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
		response := requestStatus(repository, `{"device_uuid":"`+deviceID+`","radio_access_technology":"lte","signal_bars":3}`, "device-secret")

		Expect(response.Code).To(Equal(http.StatusNoContent))
		Expect(repository.authenticatedDeviceID).To(Equal(deviceID))
		Expect(repository.authenticatedCredential).To(Equal("device-secret"))
		Expect(repository.updatedRadioDevice).To(Equal(deviceID))
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
})
