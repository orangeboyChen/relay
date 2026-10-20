package media

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

const (
	pcmSampleRate                 = 8000
	pcmFrameDuration              = 20 * time.Millisecond
	pcmSamplesPerFrame            = pcmSampleRate / 50
	pcmBytesPerFrame              = pcmSamplesPerFrame * 2
	defaultConnectionFailureGrace = 15 * time.Second
)

// Peer bridges 8 kHz mono signed PCM to a DTLS-SRTP WebRTC audio session.
type Peer struct {
	connection   *webrtc.PeerConnection
	track        *webrtc.TrackLocalStaticSample
	source       io.Reader
	sink         io.Writer
	errors       chan error
	done         chan struct{}
	startOnce    sync.Once
	closeOnce    sync.Once
	stateMu      sync.Mutex
	failureTimer *time.Timer
	failureGrace time.Duration
}

// NewPeer creates one direct WebRTC peer using the mandatory 8 kHz PCMU codec.
func NewPeer(source io.Reader, sink io.Writer) (*Peer, error) {
	settings := webrtc.SettingEngine{}
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	return newPeerWithSettings(source, sink, settings)
}

func newPeerWithSettings(source io.Reader, sink io.Writer, settings webrtc.SettingEngine) (*Peer, error) {
	return newPeer(source, sink, settings)
}

func newPeerWithICEServers(source io.Reader, sink io.Writer, settings webrtc.SettingEngine, servers []ICEServer) (*Peer, error) {
	return newPeerWithServers(source, sink, settings, servers, true)
}

func newPeer(source io.Reader, sink io.Writer, settings webrtc.SettingEngine) (*Peer, error) {
	return newPeerWithServers(source, sink, settings, nil, false)
}

func newPeerWithServers(source io.Reader, sink io.Writer, settings webrtc.SettingEngine, iceServers []ICEServer, relayOnly bool) (*Peer, error) {
	if source == nil || sink == nil {
		return nil, errors.New("WebRTC media peer requires PCM capture and playback endpoints")
	}
	engine := &webrtc.MediaEngine{}
	if err := engine.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypePCMU,
			ClockRate: pcmSampleRate,
			Channels:  1,
		},
		PayloadType: 0,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, fmt.Errorf("register PCMU codec: %w", err)
	}
	api := webrtc.NewAPI(webrtc.WithMediaEngine(engine), webrtc.WithSettingEngine(settings))
	configuration := webrtc.Configuration{}
	for _, server := range iceServers {
		if len(server.URLs) == 0 || server.Username == "" || server.Credential == "" {
			return nil, errors.New("TURN ICE server credentials are incomplete")
		}
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs:       server.URLs,
			Username:   server.Username,
			Credential: server.Credential,
		})
	}
	if relayOnly {
		configuration.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	connection, err := api.NewPeerConnection(configuration)
	if err != nil {
		return nil, fmt.Errorf("create WebRTC peer: %w", err)
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: pcmSampleRate, Channels: 1},
		"cellular-audio",
		"sim-deck-device",
	)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("create outgoing audio track: %w", err)
	}
	sender, err := connection.AddTrack(track)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("attach outgoing audio track: %w", err)
	}
	peer := &Peer{
		connection:   connection,
		track:        track,
		source:       source,
		sink:         sink,
		errors:       make(chan error, 4),
		done:         make(chan struct{}),
		failureGrace: defaultConnectionFailureGrace,
	}
	go peer.readRTCP(sender)
	connection.OnTrack(peer.receiveTrack)
	connection.OnConnectionStateChange(peer.connectionStateChanged)
	return peer, nil
}

// Answer accepts a complete SDP offer and returns an answer after ICE gathering.
func (peer *Peer) Answer(ctx context.Context, offer string) (string, error) {
	if strings.TrimSpace(offer) == "" {
		return "", errors.New("WebRTC offer must not be empty")
	}
	if err := peer.connection.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return "", fmt.Errorf("apply WebRTC offer: %w", err)
	}
	answer, err := peer.connection.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create WebRTC answer: %w", err)
	}
	gatherComplete := webrtc.GatheringCompletePromise(peer.connection)
	if err := peer.connection.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("apply WebRTC answer: %w", err)
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-gatherComplete:
	}
	local := peer.connection.LocalDescription()
	if local == nil || strings.TrimSpace(local.SDP) == "" {
		return "", errors.New("WebRTC answer is unavailable")
	}
	return local.SDP, nil
}

// Restart refreshes relay credentials and answers an ICE-restart offer without reopening modem audio.
func (peer *Peer) Restart(ctx context.Context, offer string, iceServers []ICEServer) (string, error) {
	peer.cancelFailureTimer()
	configuration := peer.connection.GetConfiguration()
	configuration.ICEServers = nil
	for _, server := range iceServers {
		if len(server.URLs) == 0 || server.Username == "" || server.Credential == "" {
			return "", errors.New("TURN ICE server credentials are incomplete")
		}
		configuration.ICEServers = append(configuration.ICEServers, webrtc.ICEServer{
			URLs: server.URLs, Username: server.Username, Credential: server.Credential,
		})
	}
	if len(iceServers) > 0 {
		configuration.ICETransportPolicy = webrtc.ICETransportPolicyRelay
	}
	if err := peer.connection.SetConfiguration(configuration); err != nil {
		return "", fmt.Errorf("refresh WebRTC configuration: %w", err)
	}
	return peer.Answer(ctx, offer)
}

// Errors reports terminal capture, playback, RTP, and RTCP failures.
func (peer *Peer) Errors() <-chan error {
	return peer.errors
}

// Close stops media processing and closes the DTLS-SRTP peer.
func (peer *Peer) Close() error {
	var closeError error
	peer.closeOnce.Do(func() {
		peer.cancelFailureTimer()
		close(peer.done)
		closeError = peer.connection.Close()
	})
	return closeError
}

func (peer *Peer) connectionStateChanged(state webrtc.PeerConnectionState) {
	switch state {
	case webrtc.PeerConnectionStateConnected:
		peer.cancelFailureTimer()
		peer.startOnce.Do(func() { go peer.capture() })
	case webrtc.PeerConnectionStateFailed:
		peer.scheduleFailure()
	case webrtc.PeerConnectionStateClosed:
		return
	default:
		return
	}
}

func (peer *Peer) scheduleFailure() {
	peer.stateMu.Lock()
	defer peer.stateMu.Unlock()
	if peer.failureTimer != nil {
		return
	}
	peer.failureTimer = time.AfterFunc(peer.failureGrace, func() {
		peer.stateMu.Lock()
		peer.failureTimer = nil
		peer.stateMu.Unlock()
		peer.report(errors.New("WebRTC peer connection failed after ICE restart grace period"))
	})
}

func (peer *Peer) cancelFailureTimer() {
	peer.stateMu.Lock()
	defer peer.stateMu.Unlock()
	if peer.failureTimer != nil {
		peer.failureTimer.Stop()
		peer.failureTimer = nil
	}
}

func (peer *Peer) capture() {
	frame := make([]byte, pcmBytesPerFrame)
	for {
		if _, err := io.ReadFull(peer.source, frame); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
				peer.report(fmt.Errorf("capture modem PCM: %w", err))
			}
			return
		}
		encoded := make([]byte, pcmSamplesPerFrame)
		for index := range encoded {
			sample := int16(binary.LittleEndian.Uint16(frame[index*2 : index*2+2]))
			encoded[index] = encodeMuLaw(sample)
		}
		if err := peer.track.WriteSample(media.Sample{Data: encoded, Duration: pcmFrameDuration}); err != nil {
			peer.report(fmt.Errorf("send modem audio: %w", err))
			return
		}
	}
}

func (peer *Peer) receiveTrack(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
	if !strings.EqualFold(track.Codec().MimeType, webrtc.MimeTypePCMU) {
		peer.report(fmt.Errorf("unsupported incoming audio codec: %s", track.Codec().MimeType))
		return
	}
	go func() {
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					peer.report(fmt.Errorf("receive phone audio: %w", err))
				}
				return
			}
			pcm := make([]byte, len(packet.Payload)*2)
			for index, value := range packet.Payload {
				binary.LittleEndian.PutUint16(pcm[index*2:index*2+2], uint16(decodeMuLaw(value)))
			}
			if _, err := peer.sink.Write(pcm); err != nil {
				peer.report(fmt.Errorf("play phone audio: %w", err))
				return
			}
		}
	}()
}

func (peer *Peer) readRTCP(sender *webrtc.RTPSender) {
	for {
		if _, _, err := sender.ReadRTCP(); err != nil {
			if !errors.Is(err, io.EOF) {
				peer.report(fmt.Errorf("read WebRTC control packets: %w", err))
			}
			return
		}
	}
}

func (peer *Peer) report(err error) {
	select {
	case <-peer.done:
		return
	case peer.errors <- err:
	default:
	}
}
