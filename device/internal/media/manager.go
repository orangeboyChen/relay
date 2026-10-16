package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

// DuplexPCM is one full-duplex modem audio endpoint.
type DuplexPCM interface {
	io.Reader
	io.Writer
	io.Closer
}

// EndpointFactory opens call-scoped modem audio.
type EndpointFactory func(context.Context) (DuplexPCM, error)

// ICEConfig controls fixed listeners used by deployed agents.
type ICEConfig struct {
	UDPPort  int
	TCPPort  int
	PublicIP string
}

type mediaPeer interface {
	Answer(context.Context, string) (string, error)
	Errors() <-chan error
	Close() error
}

type mediaPeerFactory func(io.Reader, io.Writer) (mediaPeer, error)

type activeSession struct {
	peer      mediaPeer
	endpoint  DuplexPCM
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (session *activeSession) close() error {
	session.closeOnce.Do(func() {
		close(session.done)
		session.closeErr = errors.Join(session.peer.Close(), session.endpoint.Close())
	})
	return session.closeErr
}

// Manager owns one encrypted WebRTC peer and PCM endpoint per cellular call.
type Manager struct {
	openEndpoint EndpointFactory
	newPeer      mediaPeerFactory
	errors       chan error

	mu       sync.Mutex
	sessions map[string]*activeSession
	closed   bool
	closeICE func()
}

// NewManager creates a call media manager using the production Pion peer.
func NewManager(openEndpoint EndpointFactory) (*Manager, error) {
	return NewManagerWithICE(ICEConfig{}, openEndpoint)
}

// NewManagerWithICE creates a manager with optional fixed ICE UDP/TCP listeners.
func NewManagerWithICE(config ICEConfig, openEndpoint EndpointFactory) (*Manager, error) {
	settings, closeICE, err := NewICESettings(config)
	if err != nil {
		return nil, err
	}
	manager, err := newManager(openEndpoint, func(source io.Reader, sink io.Writer) (mediaPeer, error) {
		return newPeerWithSettings(source, sink, settings)
	})
	if err != nil {
		closeICE()
		return nil, err
	}
	manager.closeICE = closeICE
	return manager, nil
}

func newManager(openEndpoint EndpointFactory, newPeer mediaPeerFactory) (*Manager, error) {
	if openEndpoint == nil || newPeer == nil {
		return nil, errors.New("media manager requires endpoint and peer factories")
	}
	return &Manager{
		openEndpoint: openEndpoint,
		newPeer:      newPeer,
		errors:       make(chan error, 8),
		sessions:     make(map[string]*activeSession),
	}, nil
}

// Negotiate opens modem audio and returns the complete Pion SDP answer.
func (manager *Manager) Negotiate(ctx context.Context, callID string, offer string) (string, error) {
	callID = strings.TrimSpace(callID)
	if callID == "" || strings.TrimSpace(offer) == "" {
		return "", errors.New("call id and WebRTC offer are required")
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return "", errors.New("media manager is closed")
	}
	if _, exists := manager.sessions[callID]; exists {
		manager.mu.Unlock()
		return "", errors.New("call media session already exists")
	}
	manager.mu.Unlock()

	endpoint, err := manager.openEndpoint(ctx)
	if err != nil {
		return "", fmt.Errorf("open modem audio: %w", err)
	}
	peer, err := manager.newPeer(endpoint, endpoint)
	if err != nil {
		_ = endpoint.Close()
		return "", fmt.Errorf("create WebRTC media peer: %w", err)
	}
	session := &activeSession{peer: peer, endpoint: endpoint, done: make(chan struct{})}
	answer, err := peer.Answer(ctx, offer)
	if err != nil {
		_ = session.close()
		return "", fmt.Errorf("answer WebRTC media offer: %w", err)
	}

	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		_ = session.close()
		return "", errors.New("media manager is closed")
	}
	if _, exists := manager.sessions[callID]; exists {
		manager.mu.Unlock()
		_ = session.close()
		return "", errors.New("call media session already exists")
	}
	manager.sessions[callID] = session
	manager.mu.Unlock()
	go manager.monitor(callID, session)
	return answer, nil
}

// Stop closes media for one call and leaves other call sessions intact.
func (manager *Manager) Stop(callID string) error {
	callID = strings.TrimSpace(callID)
	manager.mu.Lock()
	session := manager.sessions[callID]
	delete(manager.sessions, callID)
	manager.mu.Unlock()
	if session == nil {
		return nil
	}
	return session.close()
}

// Errors reports terminal media failures after their resources are closed.
func (manager *Manager) Errors() <-chan error {
	return manager.errors
}

// Close prevents new negotiations and closes every active call session.
func (manager *Manager) Close() error {
	manager.mu.Lock()
	manager.closed = true
	sessions := make([]*activeSession, 0, len(manager.sessions))
	for callID, session := range manager.sessions {
		sessions = append(sessions, session)
		delete(manager.sessions, callID)
	}
	manager.mu.Unlock()
	var closeError error
	for _, session := range sessions {
		closeError = errors.Join(closeError, session.close())
	}
	if manager.closeICE != nil {
		manager.closeICE()
	}
	return closeError
}

func (manager *Manager) monitor(callID string, session *activeSession) {
	select {
	case <-session.done:
		return
	case err := <-session.peer.Errors():
		manager.mu.Lock()
		if manager.sessions[callID] == session {
			delete(manager.sessions, callID)
		}
		manager.mu.Unlock()
		_ = session.close()
		manager.report(fmt.Errorf("call media %s: %w", callID, err))
	}
}

func (manager *Manager) report(err error) {
	select {
	case manager.errors <- err:
	default:
	}
}
