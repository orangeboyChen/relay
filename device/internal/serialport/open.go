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
func Open(endpoint string, baud int, tlsCAFile string, tlsCertificateFile string, tlsKeyFile string) (io.ReadWriteCloser, error) {
	if strings.HasPrefix(endpoint, "tls://") {
		return openTLSBridge(endpoint, tlsCAFile, tlsCertificateFile, tlsKeyFile)
	}
	if strings.Contains(endpoint, "://") {
		return nil, fmt.Errorf("unsupported AT endpoint scheme")
	}
	if strings.TrimSpace(tlsCAFile) != "" || strings.TrimSpace(tlsCertificateFile) != "" || strings.TrimSpace(tlsKeyFile) != "" {
		return nil, fmt.Errorf("AT TLS credentials require a tls:// endpoint")
	}
	return openTTY(endpoint, baud)
}

func openTLSBridge(endpoint string, caFile string, certificateFile string, keyFile string) (io.ReadWriteCloser, error) {
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
	certificateFile = strings.TrimSpace(certificateFile)
	keyFile = strings.TrimSpace(keyFile)
	if certificateFile == "" || keyFile == "" {
		return nil, fmt.Errorf("AT TLS client certificate and key files are required")
	}
	certificate, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read AT TLS CA file: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, fmt.Errorf("AT TLS CA file contains no certificates")
	}
	clientCertificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load AT TLS client identity: %w", err)
	}
	dialer := &net.Dialer{Timeout: bridgeDialTimeout}
	connection, err := tls.DialWithDialer(dialer, "tcp", parsed.Host, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		ServerName:   parsed.Hostname(),
		Certificates: []tls.Certificate{clientCertificate},
	})
	if err != nil {
		return nil, fmt.Errorf("connect AT TLS bridge: %w", err)
	}
	return connection, nil
}
