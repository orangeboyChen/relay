package api

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// TURNCredentials is a short-lived coturn REST-auth credential.
type TURNCredentials struct {
	URLs       []string  `json:"urls"`
	Username   string    `json:"username"`
	Credential string    `json:"credential"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// NewTURNCredentials creates credentials compatible with coturn's use-auth-secret mode.
func NewTURNCredentials(url string, secret []byte, now time.Time, ttl time.Duration) TURNCredentials {
	expiresAt := now.UTC().Add(ttl)
	username := strconv.FormatInt(expiresAt.Unix(), 10) + ":sim-deck"
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write([]byte(username))
	return TURNCredentials{
		URLs:       turnURLs(url),
		Username:   username,
		Credential: base64.StdEncoding.EncodeToString(mac.Sum(nil)),
		ExpiresAt:  expiresAt,
	}
}

func turnURLs(serverURL string) []string {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || parsed.Scheme == "turns" {
		return []string{serverURL}
	}
	query := parsed.Query()
	query.Set("transport", "udp")
	parsed.RawQuery = query.Encode()
	udpURL := parsed.String()
	query.Set("transport", "tcp")
	parsed.RawQuery = query.Encode()
	return []string{udpURL, parsed.String()}
}
