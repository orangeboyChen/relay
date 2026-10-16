package serialport

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const bridgeDialTimeout = 5 * time.Second

// Open opens either a local modem TTY or a CA-authenticated TLS bridge.
func Open(endpoint string, baud int, tlsCAFile string) (io.ReadWriteCloser, error) {
	if strings.HasPrefix(endpoint, "tls://") {
		return openTLSBridge(endpoint, tlsCAFile)
	}
	if strings.Contains(endpoint, "://") {
		return nil, fmt.Errorf("unsupported AT endpoint scheme")
	}
	if strings.TrimSpace(tlsCAFile) != "" {
		return nil, fmt.Errorf("AT TLS CA file requires a tls:// endpoint")
	}
	return openTTY(endpoint, baud)
}

func openTLSBridge(endpoint string, caFile string) (io.ReadWriteCloser, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "tls" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, fmt.Errorf("AT TLS endpoint must be tls://host:port")
	}
	if parsed.Port() == "" {
		return nil, fmt.Errorf("AT TLS endpoint must include a port")
	}
	caFile = strings.TrimSpace(caFile)
	if caFile == "" {
		return nil, fmt.Errorf("AT TLS CA file is required")
	}
	certificate, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read AT TLS CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, fmt.Errorf("AT TLS CA file contains no certificates")
	}
	dialer := &net.Dialer{Timeout: bridgeDialTimeout}
	connection, err := tls.DialWithDialer(dialer, "tcp", parsed.Host, &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
		ServerName: parsed.Hostname(),
	})
	if err != nil {
		return nil, fmt.Errorf("connect AT TLS bridge: %w", err)
	}
	return connection, nil
}
