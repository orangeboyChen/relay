package media

import (
	"bytes"
	"net"
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
