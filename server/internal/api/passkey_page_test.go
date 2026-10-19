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
	page := response.Body.String()
	for _, endpoint := range []string{
		"'/v1/auth/passkey/'+operation+'/options'",
		"'/v1/auth/passkey/'+operation+'/finish?ceremony_id='",
	} {
		if !strings.Contains(page, endpoint) {
			t.Fatalf("expected passkey page to construct endpoint %q", endpoint)
		}
	}
	if !strings.Contains(page, "operation=mode==='register'?'registration':'login'") {
		t.Fatal("expected register mode to use the registration API operation")
	}
	if strings.Contains(page, "'/v1/auth/passkey/'+mode+'/options'") || strings.Contains(page, "'/v1/auth/passkey/'+mode+'/finish") {
		t.Fatal("passkey page must not use the unsupported register API path")
	}
}
