package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/pion/ice/v4"
	"github.com/pion/logging"
	"github.com/pion/transport/v3/vnet"
	"github.com/pion/webrtc/v4"
	pionmedia "github.com/pion/webrtc/v4/pkg/media"
)

var _ = Describe("Pion cellular audio peer", func() {
	It("answers an audio offer with PCMU and complete ICE candidates", func() {
		peer, err := NewPeer(bytes.NewReader(nil), &bytes.Buffer{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(peer.Close()).To(Succeed()) })

		offerer, offer := newOfferer()
		DeferCleanup(func() { Expect(offerer.Close()).To(Succeed()) })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		answer, err := peer.Answer(ctx, offer)

		Expect(err).NotTo(HaveOccurred())
		Expect(answer).To(ContainSubstring("m=audio"))
		Expect(answer).To(ContainSubstring("a=rtpmap:0 PCMU/8000"))
		Expect(answer).To(ContainSubstring("a=end-of-candidates"))
		Expect(peer.Errors()).NotTo(BeNil())
	})

	It("rejects missing and malformed offers", func() {
		peer, err := NewPeer(bytes.NewReader(nil), &bytes.Buffer{})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = peer.Close() })
		_, err = peer.Answer(context.Background(), " ")
		Expect(err).To(MatchError("WebRTC offer must not be empty"))
		_, err = peer.Answer(context.Background(), "not SDP")
		Expect(err).To(MatchError(ContainSubstring("apply WebRTC offer")))
	})

	It("validates PCM endpoints and closes idempotently", func() {
		_, err := NewPeer(nil, &bytes.Buffer{})
		Expect(err).To(HaveOccurred())
		_, err = NewPeer(bytes.NewReader(nil), nil)
		Expect(err).To(HaveOccurred())

		peer, err := NewPeer(bytes.NewReader(nil), &bytes.Buffer{})
		Expect(err).NotTo(HaveOccurred())
		Expect(peer.Close()).To(Succeed())
		Expect(peer.Close()).To(Succeed())
	})

	It("bridges one PCM frame in both directions over DTLS-SRTP", func() {
		wan, offerSettings, answerSettings := virtualNetwork()
		DeferCleanup(func() { Expect(wan.Stop()).To(Succeed()) })
		capturedPCM := make([]byte, pcmBytesPerFrame)
		for index := 0; index < pcmSamplesPerFrame; index++ {
			binary.LittleEndian.PutUint16(capturedPCM[index*2:index*2+2], uint16(int16(1000)))
		}
		playback := &recordingWriter{writes: make(chan []byte, 1)}
		peer, err := newPeer(bytes.NewReader(capturedPCM), playback, answerSettings)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = peer.Close() })

		offerer, outgoing, incoming, connected, offer := newBidirectionalOfferer(offerSettings)
		DeferCleanup(func() { _ = offerer.Close() })
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		answer, err := peer.Answer(ctx, offer)
		Expect(err).NotTo(HaveOccurred())
		Expect(offerer.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer})).To(Succeed())
		Eventually(connected, 5*time.Second).Should(BeClosed())

		var remote *webrtc.TrackRemote
		Eventually(incoming, 5*time.Second).Should(Receive(&remote))
		packet, _, err := remote.ReadRTP()
		Expect(err).NotTo(HaveOccurred())
		Expect(packet.Payload).To(HaveLen(pcmSamplesPerFrame))
		Expect(decodeMuLaw(packet.Payload[0])).To(BeNumerically("~", 1000, 20))

		phoneAudio := bytes.Repeat([]byte{encodeMuLaw(-1000)}, pcmSamplesPerFrame)
		Expect(outgoing.WriteSample(pionmedia.Sample{Data: phoneAudio, Duration: pcmFrameDuration})).To(Succeed())
		var played []byte
		Eventually(playback.writes, 5*time.Second).Should(Receive(&played))
		Expect(played).To(HaveLen(pcmBytesPerFrame))
		Expect(int16(binary.LittleEndian.Uint16(played[:2]))).To(BeNumerically("~", -1000, 20))

		playback.fail.Store(true)
		Expect(outgoing.WriteSample(pionmedia.Sample{Data: phoneAudio, Duration: pcmFrameDuration})).To(Succeed())
		Eventually(peer.Errors()).Should(Receive(MatchError("play phone audio: playback failed")))
	})

	It("reports capture and connection failures without blocking", func() {
		peer, err := NewPeer(failingReader{}, io.Discard)
		Expect(err).NotTo(HaveOccurred())
		peer.connectionStateChanged(webrtc.PeerConnectionStateConnected)
		Eventually(peer.Errors()).Should(Receive(MatchError("capture modem PCM: capture failed")))
		peer.connectionStateChanged(webrtc.PeerConnectionStateFailed)
		Eventually(peer.Errors()).Should(Receive(MatchError("WebRTC peer connection failed")))
		Expect(peer.Close()).To(Succeed())
		peer.report(errors.New("ignored after close"))
	})
})

func newOfferer() (*webrtc.PeerConnection, string) {
	engine := &webrtc.MediaEngine{}
	Expect(engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: pcmSampleRate, Channels: 1},
		PayloadType:        0,
	}, webrtc.RTPCodecTypeAudio)).To(Succeed())
	settings := webrtc.SettingEngine{}
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	offerer, err := webrtc.NewAPI(
		webrtc.WithMediaEngine(engine),
		webrtc.WithSettingEngine(settings),
	).NewPeerConnection(webrtc.Configuration{})
	Expect(err).NotTo(HaveOccurred())
	_, err = offerer.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio)
	Expect(err).NotTo(HaveOccurred())
	offer, err := offerer.CreateOffer(nil)
	Expect(err).NotTo(HaveOccurred())
	gatherComplete := webrtc.GatheringCompletePromise(offerer)
	Expect(offerer.SetLocalDescription(offer)).To(Succeed())
	Eventually(gatherComplete, 5*time.Second).Should(BeClosed())
	local := offerer.LocalDescription()
	Expect(local).NotTo(BeNil())
	Expect(strings.TrimSpace(local.SDP)).NotTo(BeEmpty())
	return offerer, local.SDP
}

func newBidirectionalOfferer(settings webrtc.SettingEngine) (*webrtc.PeerConnection, *webrtc.TrackLocalStaticSample, <-chan *webrtc.TrackRemote, <-chan struct{}, string) {
	engine := &webrtc.MediaEngine{}
	Expect(engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: pcmSampleRate, Channels: 1},
		PayloadType:        0,
	}, webrtc.RTPCodecTypeAudio)).To(Succeed())
	offerer, err := webrtc.NewAPI(
		webrtc.WithMediaEngine(engine),
		webrtc.WithSettingEngine(settings),
	).NewPeerConnection(webrtc.Configuration{})
	Expect(err).NotTo(HaveOccurred())
	outgoing, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: pcmSampleRate, Channels: 1},
		"phone-audio",
		"sim-deck-ios",
	)
	Expect(err).NotTo(HaveOccurred())
	sender, err := offerer.AddTrack(outgoing)
	Expect(err).NotTo(HaveOccurred())
	go func() { _, _, _ = sender.ReadRTCP() }()
	incoming := make(chan *webrtc.TrackRemote, 1)
	offerer.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) { incoming <- track })
	connected := make(chan struct{})
	offerer.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case <-connected:
			default:
				close(connected)
			}
		}
	})
	offer, err := offerer.CreateOffer(nil)
	Expect(err).NotTo(HaveOccurred())
	gatherComplete := webrtc.GatheringCompletePromise(offerer)
	Expect(offerer.SetLocalDescription(offer)).To(Succeed())
	Eventually(gatherComplete, 5*time.Second).Should(BeClosed())
	local := offerer.LocalDescription()
	Expect(local).NotTo(BeNil())
	return offerer, outgoing, incoming, connected, local.SDP
}

type recordingWriter struct {
	writes chan []byte
	fail   atomic.Bool
}

func (writer *recordingWriter) Write(value []byte) (int, error) {
	if writer.fail.Load() {
		return 0, errors.New("playback failed")
	}
	copyValue := append([]byte(nil), value...)
	writer.writes <- copyValue
	return len(value), nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("capture failed") }

func virtualNetwork() (*vnet.Router, webrtc.SettingEngine, webrtc.SettingEngine) {
	wan, err := vnet.NewRouter(&vnet.RouterConfig{
		CIDR:          "1.2.3.0/24",
		LoggerFactory: logging.NewDefaultLoggerFactory(),
	})
	Expect(err).NotTo(HaveOccurred())
	offerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"1.2.3.4"}})
	Expect(err).NotTo(HaveOccurred())
	Expect(wan.AddNet(offerNet)).To(Succeed())
	answerNet, err := vnet.NewNet(&vnet.NetConfig{StaticIPs: []string{"1.2.3.5"}})
	Expect(err).NotTo(HaveOccurred())
	Expect(wan.AddNet(answerNet)).To(Succeed())
	Expect(wan.Start()).To(Succeed())
	offerSettings := webrtc.SettingEngine{}
	offerSettings.SetNet(offerNet)
	offerSettings.SetICETimeouts(time.Second, time.Second, 200*time.Millisecond)
	answerSettings := webrtc.SettingEngine{}
	answerSettings.SetNet(answerNet)
	answerSettings.SetICETimeouts(time.Second, time.Second, 200*time.Millisecond)
	return wan, offerSettings, answerSettings
}
