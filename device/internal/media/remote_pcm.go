package media

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
)

const (
	pcmProtocolMagic     = "SDP1"
	pcmHeaderSize        = 9
	pcmCaptureDirection  = 0
	pcmPlaybackDirection = 1
	maxRemotePCMFrame    = 4096
)

// OpenRemotePCMEndpoint connects to the macOS USB audio bridge over mutual TLS.
func OpenRemotePCMEndpoint(ctx context.Context, endpointURL, caFile, certificateFile, keyFile string) (*PCMEndpoint, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpointURL))
	if err != nil || parsed.Scheme != "tls" || parsed.Host == "" || parsed.Port() == "" {
		return nil, errors.New("remote PCM endpoint must be tls://host:port")
	}
	rootBytes, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read remote PCM CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(rootBytes) {
		return nil, errors.New("remote PCM CA does not contain a certificate")
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load remote PCM client identity: %w", err)
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", parsed.Host)
	if err != nil {
		return nil, fmt.Errorf("dial remote PCM bridge: %w", err)
	}
	tlsConnection := tls.Client(connection, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      roots,
		Certificates: []tls.Certificate{certificate},
		ServerName:   parsed.Hostname(),
	})
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("handshake remote PCM bridge: %w", err)
	}
	remote := &remotePCMConnection{connection: tlsConnection}
	return &PCMEndpoint{
		source: remote,
		sink:   remote,
		close:  remote.Close,
	}, nil
}

type remotePCMConnection struct {
	connection net.Conn
	readMu     sync.Mutex
	writeMu    sync.Mutex
	pending    []byte
	closed     sync.Once
}

func (remote *remotePCMConnection) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	remote.readMu.Lock()
	defer remote.readMu.Unlock()
	for len(remote.pending) == 0 {
		payload, err := readPCMFrame(remote.connection, pcmCaptureDirection)
		if err != nil {
			return 0, err
		}
		remote.pending = payload
	}
	count := copy(buffer, remote.pending)
	remote.pending = remote.pending[count:]
	return count, nil
}

func (remote *remotePCMConnection) Write(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(buffer) > maxRemotePCMFrame {
		return 0, fmt.Errorf("remote PCM frame is too large: %d", len(buffer))
	}
	remote.writeMu.Lock()
	defer remote.writeMu.Unlock()
	if err := writePCMFrame(remote.connection, pcmPlaybackDirection, buffer); err != nil {
		return 0, err
	}
	return len(buffer), nil
}

func (remote *remotePCMConnection) Close() error {
	var err error
	remote.closed.Do(func() { err = remote.connection.Close() })
	return err
}

func readPCMFrame(reader io.Reader, expectedDirection byte) ([]byte, error) {
	header := make([]byte, pcmHeaderSize)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if string(header[:4]) != pcmProtocolMagic {
		return nil, errors.New("remote PCM frame has an invalid magic")
	}
	if header[4] != expectedDirection {
		return nil, fmt.Errorf("remote PCM frame direction %d does not match %d", header[4], expectedDirection)
	}
	length := binary.BigEndian.Uint32(header[5:])
	if length == 0 || length > maxRemotePCMFrame {
		return nil, fmt.Errorf("remote PCM frame length %d is invalid", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writePCMFrame(writer io.Writer, direction byte, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxRemotePCMFrame {
		return fmt.Errorf("remote PCM frame length %d is invalid", len(payload))
	}
	header := make([]byte, pcmHeaderSize)
	copy(header, pcmProtocolMagic)
	header[4] = direction
	binary.BigEndian.PutUint32(header[5:], uint32(len(payload)))
	if err := writeFull(writer, header); err != nil {
		return err
	}
	return writeFull(writer, payload)
}

func writeFull(writer io.Writer, payload []byte) error {
	for len(payload) > 0 {
		count, err := writer.Write(payload)
		if err != nil {
			return err
		}
		if count <= 0 {
			return io.ErrShortWrite
		}
		payload = payload[count:]
	}
	return nil
}
