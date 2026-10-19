package api

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type requestLogWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int
	errorCode    string
	errorMessage string
}

func (writer *requestLogWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *requestLogWriter) Write(value []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(value)
	writer.bytesWritten += written
	return written, err
}

func (writer *requestLogWriter) recordAPIError(code string, message string) {
	writer.errorCode = code
	writer.errorMessage = message
}

func (writer *requestLogWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return writer.ResponseWriter.(http.Hijacker).Hijack()
}

func (writer *requestLogWriter) Flush() {
	_ = http.NewResponseController(writer.ResponseWriter).Flush()
}

func (writer *requestLogWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func withRequestLogging(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := request.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		response.Header().Set("X-Request-ID", requestID)
		writer := &requestLogWriter{ResponseWriter: response}
		next.ServeHTTP(writer, request)
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		attributes := []any{
			"request_id", requestID,
			"method", request.Method,
			"path", request.URL.Path,
			"status", status,
			"bytes", writer.bytesWritten,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		}
		if writer.errorCode != "" {
			attributes = append(attributes, "error_code", writer.errorCode, "error_message", writer.errorMessage)
		}
		switch {
		case status >= http.StatusInternalServerError:
			logger.Error("HTTP request failed", attributes...)
		case status >= http.StatusBadRequest:
			logger.Warn("HTTP request rejected", attributes...)
		default:
			logger.Info("HTTP request completed", attributes...)
		}
	})
}
