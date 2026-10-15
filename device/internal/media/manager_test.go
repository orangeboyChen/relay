package media

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Call media manager", func() {
	It("negotiates and closes one call-scoped media session", func() {
		endpoint := &fakeDuplexPCM{}
		peer := newFakeMediaPeer("answer-sdp")
		manager, err := newManager(
			func(context.Context) (DuplexPCM, error) { return endpoint, nil },
			func(source io.Reader, sink io.Writer) (mediaPeer, error) {
				Expect(source).To(BeIdenticalTo(endpoint))
				Expect(sink).To(BeIdenticalTo(endpoint))
				return peer, nil
			},
		)
		Expect(err).NotTo(HaveOccurred())

		answer, err := manager.Negotiate(context.Background(), "call-1", "offer-sdp")

		Expect(err).NotTo(HaveOccurred())
		Expect(answer).To(Equal("answer-sdp"))
		Expect(peer.offer).To(Equal("offer-sdp"))
		Expect(manager.Stop("call-1")).To(Succeed())
		Expect(manager.Stop("call-1")).To(Succeed())
		Expect(peer.closeCount).To(Equal(1))
		Expect(endpoint.closeCount).To(Equal(1))
	})

	It("rejects invalid, duplicate, and post-close negotiations", func() {
		manager, err := newManager(
			func(context.Context) (DuplexPCM, error) { return &fakeDuplexPCM{}, nil },
			func(io.Reader, io.Writer) (mediaPeer, error) { return newFakeMediaPeer("answer"), nil },
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), " ", "offer")
		Expect(err).To(MatchError("call id and WebRTC offer are required"))
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).To(MatchError("call media session already exists"))
		Expect(manager.Close()).To(Succeed())
		_, err = manager.Negotiate(context.Background(), "call-2", "offer")
		Expect(err).To(MatchError("media manager is closed"))
	})

	It("cleans up endpoint, peer creation, and answer failures", func() {
		manager, err := newManager(
			func(context.Context) (DuplexPCM, error) { return nil, errors.New("audio unavailable") },
			func(io.Reader, io.Writer) (mediaPeer, error) { return nil, nil },
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).To(MatchError("open modem audio: audio unavailable"))

		endpoint := &fakeDuplexPCM{}
		manager, err = newManager(
			func(context.Context) (DuplexPCM, error) { return endpoint, nil },
			func(io.Reader, io.Writer) (mediaPeer, error) { return nil, errors.New("peer unavailable") },
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).To(MatchError("create WebRTC media peer: peer unavailable"))
		Expect(endpoint.closeCount).To(Equal(1))

		endpoint = &fakeDuplexPCM{}
		peer := newFakeMediaPeer("")
		peer.answerError = errors.New("invalid offer")
		manager, err = newManager(
			func(context.Context) (DuplexPCM, error) { return endpoint, nil },
			func(io.Reader, io.Writer) (mediaPeer, error) { return peer, nil },
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).To(MatchError("answer WebRTC media offer: invalid offer"))
		Expect(peer.closeCount).To(Equal(1))
		Expect(endpoint.closeCount).To(Equal(1))
	})

	It("reports peer failures after closing the affected session", func() {
		endpoint := &fakeDuplexPCM{}
		peer := newFakeMediaPeer("answer")
		manager, err := newManager(
			func(context.Context) (DuplexPCM, error) { return endpoint, nil },
			func(io.Reader, io.Writer) (mediaPeer, error) { return peer, nil },
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).NotTo(HaveOccurred())

		peer.errors <- errors.New("ICE failed")

		Eventually(manager.Errors()).Should(Receive(MatchError("call media call-1: ICE failed")))
		Eventually(peer.CloseCount).Should(Equal(1))
		Expect(endpoint.CloseCount()).To(Equal(1))
		Expect(manager.Stop("call-1")).To(Succeed())
	})

	It("validates factories and joins close failures", func() {
		_, err := NewManager(nil)
		Expect(err).To(HaveOccurred())
		_, err = newManager(func(context.Context) (DuplexPCM, error) { return &fakeDuplexPCM{}, nil }, nil)
		Expect(err).To(HaveOccurred())

		endpoint := &fakeDuplexPCM{closeError: errors.New("endpoint close failed")}
		peer := newFakeMediaPeer("answer")
		peer.closeError = errors.New("peer close failed")
		manager, err := newManager(
			func(context.Context) (DuplexPCM, error) { return endpoint, nil },
			func(io.Reader, io.Writer) (mediaPeer, error) { return peer, nil },
		)
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Negotiate(context.Background(), "call-1", "offer")
		Expect(err).NotTo(HaveOccurred())
		Expect(manager.Close()).To(MatchError(And(
			ContainSubstring("peer close failed"),
			ContainSubstring("endpoint close failed"),
		)))
		Expect(manager.Close()).To(Succeed())
	})
})

type fakeDuplexPCM struct {
	bytes.Buffer
	closeError error
	closeCount int
	mu         sync.Mutex
}

func (endpoint *fakeDuplexPCM) Close() error {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	endpoint.closeCount++
	return endpoint.closeError
}

func (endpoint *fakeDuplexPCM) CloseCount() int {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()
	return endpoint.closeCount
}

type fakeMediaPeer struct {
	answer      string
	answerError error
	offer       string
	errors      chan error
	closeError  error
	closeCount  int
	mu          sync.Mutex
}

func newFakeMediaPeer(answer string) *fakeMediaPeer {
	return &fakeMediaPeer{answer: answer, errors: make(chan error, 1)}
}

func (peer *fakeMediaPeer) Answer(_ context.Context, offer string) (string, error) {
	peer.offer = offer
	return peer.answer, peer.answerError
}

func (peer *fakeMediaPeer) Errors() <-chan error {
	return peer.errors
}

func (peer *fakeMediaPeer) Close() error {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	peer.closeCount++
	return peer.closeError
}

func (peer *fakeMediaPeer) CloseCount() int {
	peer.mu.Lock()
	defer peer.mu.Unlock()
	return peer.closeCount
}
