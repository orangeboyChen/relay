package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPasskeyPage(t *testing.T) {
	router := &router{}
	request := httptest.NewRequest(http.MethodGet, "/v1/auth/passkey?mode=login", nil)
	response := httptest.NewRecorder()

	router.passkeyPage(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("unexpected content type: %q", got)
	}
	if !strings.Contains(response.Body.String(), "Continue with passkey") {
		t.Fatal("expected passkey action in page")
	}
}
