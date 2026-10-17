package media

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Remote PCM framing", func() {
	It("round trips capture and playback frames", func() {
		var encoded bytes.Buffer
		Expect(writePCMFrame(&encoded, pcmCaptureDirection, []byte("capture"))).To(Succeed())
		remote := &remotePCMConnection{connection: &bufferConn{Buffer: &encoded}}
		buffer := make([]byte, 7)
		_, err := remote.Read(buffer)
		Expect(err).NotTo(HaveOccurred())
		Expect(buffer).To(Equal([]byte("capture")))
		encoded.Reset()
		remote = &remotePCMConnection{connection: &bufferConn{Buffer: &encoded}}
		_, err = remote.Write([]byte("playback"))
		Expect(err).NotTo(HaveOccurred())
		Expect(encoded.Bytes()[4]).To(Equal(byte(pcmPlaybackDirection)))
	})

	DescribeTable("rejects invalid frames", func(frame []byte, direction byte, message string) {
		_, err := readPCMFrame(bytes.NewReader(frame), direction)
		Expect(err).To(MatchError(message))
	},
		Entry("bad magic", []byte("BAD!\x00\x00\x00\x01\x00"), byte(pcmCaptureDirection), "remote PCM frame has an invalid magic"),
		Entry("wrong direction", append([]byte("SDP1\x01"), 0, 0, 0, 1, 0), byte(pcmCaptureDirection), "remote PCM frame direction 1 does not match 0"),
		Entry("empty payload", append([]byte("SDP1\x00"), 0, 0, 0, 0), byte(pcmCaptureDirection), "remote PCM frame length 0 is invalid"),
	)

	It("rejects oversized playback frames", func() {
		var encoded bytes.Buffer
		remote := &remotePCMConnection{connection: &bufferConn{Buffer: &encoded}}
		_, err := remote.Write(make([]byte, maxRemotePCMFrame+1))
		Expect(err).To(MatchError("remote PCM frame is too large: 4097"))
	})

	It("handles empty reads and rejects truncated frames", func() {
		remote := &remotePCMConnection{connection: &bufferConn{Buffer: bytes.NewBuffer(nil)}}
		count, err := remote.Read(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(count).To(Equal(0))

		_, err = readPCMFrame(bytes.NewReader([]byte("SDP1\x00\x00\x00\x00\x02x")), pcmCaptureDirection)
		Expect(err).To(MatchError(io.ErrUnexpectedEOF))
	})

	DescribeTable("rejects invalid writes", func(payload []byte, message string) {
		var encoded bytes.Buffer
		Expect(writePCMFrame(&encoded, pcmPlaybackDirection, payload)).To(MatchError(message))
	},
		Entry("empty payload", []byte{}, "remote PCM frame length 0 is invalid"),
		Entry("oversized payload", make([]byte, maxRemotePCMFrame+1), "remote PCM frame length 4097 is invalid"),
	)

	It("returns writer failures and short writes", func() {
		writer := &failingPCMWriter{err: errors.New("write failed")}
		Expect(writePCMFrame(writer, pcmPlaybackDirection, []byte("audio"))).To(MatchError("write failed"))

		shortWriter := &shortPCMWriter{}
		Expect(writePCMFrame(shortWriter, pcmPlaybackDirection, []byte("audio"))).To(MatchError(io.ErrShortWrite))
	})

	It("validates remote PCM endpoint configuration before dialing", func() {
		_, err := OpenRemotePCMEndpoint(nil, "http://bridge:19002", "", "", "")
		Expect(err).To(MatchError("remote PCM endpoint must be tls://host:port"))

		_, err = OpenRemotePCMEndpoint(nil, "tls://bridge:19002", "/missing/ca.pem", "", "")
		Expect(err).To(MatchError(ContainSubstring("read remote PCM CA")))

		caFile := filepath.Join(GinkgoT().TempDir(), "ca.pem")
		Expect(os.WriteFile(caFile, []byte("not a certificate"), 0o600)).To(Succeed())
		_, err = OpenRemotePCMEndpoint(nil, "tls://bridge:19002", caFile, "", "")
		Expect(err).To(MatchError("remote PCM CA does not contain a certificate"))
	})

	It("closes a remote PCM connection only once", func() {
		connection := &bufferConn{Buffer: bytes.NewBuffer(nil)}
		remote := &remotePCMConnection{connection: connection}
		Expect(remote.Close()).To(Succeed())
		Expect(remote.Close()).To(Succeed())
	})
})

type bufferConn struct {
	*bytes.Buffer
}

func (connection *bufferConn) Close() error                     { return nil }
func (connection *bufferConn) LocalAddr() net.Addr              { return bufferAddr{} }
func (connection *bufferConn) RemoteAddr() net.Addr             { return bufferAddr{} }
func (connection *bufferConn) SetDeadline(time.Time) error      { return nil }
func (connection *bufferConn) SetReadDeadline(time.Time) error  { return nil }
func (connection *bufferConn) SetWriteDeadline(time.Time) error { return nil }

type bufferAddr struct{}

func (bufferAddr) Network() string { return "buffer" }
func (bufferAddr) String() string  { return "buffer" }

type failingPCMWriter struct{ err error }

func (writer *failingPCMWriter) Write([]byte) (int, error) { return 0, writer.err }

type shortPCMWriter struct{}

func (*shortPCMWriter) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	return 0, nil
}
