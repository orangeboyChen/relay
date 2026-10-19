package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Request logging", func() {
	It("logs request correlation and sanitized API errors", func() {
		var output bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&output, nil))
		handler := withRequestLogging(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeError(writer, http.StatusInternalServerError, "storage_error", "Unable to update radio status.")
		}), logger)
		request := httptest.NewRequest(http.MethodPut, "/v1/device-radio-status?secret=hidden", nil)
		request.Header.Set("Authorization", "Bearer must-not-be-logged")
		request.Header.Set("X-Request-ID", "radio-test-request")
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Header().Get("X-Request-ID")).To(Equal("radio-test-request"))
		Expect(output.String()).To(ContainSubstring(`"msg":"HTTP request failed"`))
		Expect(output.String()).To(ContainSubstring(`"path":"/v1/device-radio-status"`))
		Expect(output.String()).To(ContainSubstring(`"error_code":"storage_error"`))
		Expect(output.String()).NotTo(ContainSubstring("must-not-be-logged"))
		Expect(output.String()).NotTo(ContainSubstring("secret=hidden"))
	})

	It("generates a request ID and records successful response size", func() {
		var output bytes.Buffer
		handler := withRequestLogging(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte("ok"))
		}), slog.New(slog.NewJSONHandler(&output, nil)))
		response := httptest.NewRecorder()

		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Header().Get("X-Request-ID")).NotTo(BeEmpty())
		Expect(output.String()).To(ContainSubstring(`"bytes":2`))
	})

	It("exposes response-controller flush and unwrap behavior", func() {
		response := httptest.NewRecorder()
		writer := &requestLogWriter{ResponseWriter: response}

		writer.Flush()

		Expect(writer.Unwrap()).To(BeIdenticalTo(response))
		Expect(response.Flushed).To(BeTrue())
	})
})
