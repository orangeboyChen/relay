package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	communicationv1 "github.com/orangeboyChen/relay-sdk/gen/go/relay/communication/v1"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Connect encrypted sync service", func() {
	var (
		now        time.Time
		repository *fakeRepository
		handler    http.Handler
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
		repository = &fakeRepository{}
		handler = Handler(repository, Config{Now: func() time.Time { return now }})
	})

	request := func(procedure, authorization, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost,
			"/relay.communication.v1.CommunicationSyncService/"+procedure,
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	It("rejects absent and malformed bearer credentials before owner authentication", func() {
		for _, authorization := range []string{"", "Basic owner-token", "Bearer owner-token extra"} {
			response := request("PullEvents", authorization, `{}`)
			Expect(response.Code).To(Equal(http.StatusUnauthorized), authorization)
		}
		Expect(repository.ownerAuthToken).To(BeEmpty())
	})

	It("accepts a well-formed bearer credential and returns an empty event page", func() {
		response := request("PullEvents", "Bearer owner-token", `{"afterSequence":"0","pageSize":10}`)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(Equal("{}"))
		Expect(repository.ownerAuthToken).To(Equal("owner-token"))
	})

	It("reads an owner-scoped encrypted history snapshot", func() {
		repository.ownerID = "owner-7"
		repository.historySnapshot = domain.HistorySnapshot{
			Revision: 3, Ciphertext: []byte{1, 2, 3}, UpdatedAt: now,
		}

		response := request("GetHistorySnapshot", "Bearer owner-token", `{"kind":"HISTORY_KIND_MESSAGES"}`)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"revision":"3"`))
		Expect(response.Body.String()).To(ContainSubstring(`"ciphertext":"AQID"`))
	})

	It("rejects client snapshot writes", func() {
		response := request("PutHistorySnapshot", "Bearer owner-token",
			`{"kind":"HISTORY_KIND_CALLS","expectedRevision":"4","ciphertext":"AQID"}`)

		Expect(response.Code).To(Equal(http.StatusNotImplemented))
		Expect(response.Body.String()).To(ContainSubstring("read-only"))
		Expect(repository.historyWriteCiphertext).To(BeNil())
	})

	It("does not expose repository errors for snapshot reads", func() {
		repository.historySnapshotError = errors.New("private storage detail")

		response := request("GetHistorySnapshot", "Bearer owner-token", `{"kind":"HISTORY_KIND_MESSAGES"}`)

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).NotTo(ContainSubstring("private storage detail"))
	})
})

type scriptedWatchEventsStream struct {
	first     *communicationv1.WatchEventsRequest
	requests  chan *communicationv1.WatchEventsRequest
	responses chan *communicationv1.WatchEventsResponse
}

func (stream *scriptedWatchEventsStream) Receive() (*communicationv1.WatchEventsRequest, error) {
	if stream.first != nil {
		request := stream.first
		stream.first = nil
		return request, nil
	}
	request, ok := <-stream.requests
	if !ok {
		return nil, io.EOF
	}
	return request, nil
}

func (stream *scriptedWatchEventsStream) Send(response *communicationv1.WatchEventsResponse) error {
	stream.responses <- response
	return nil
}

func TestWatchEventsWaitsForDurableAcknowledgement(t *testing.T) {
	now := time.Now().UTC()
	events := make([]domain.EventEnvelope, 101)
	for index := range events {
		sequence := int64(index + 1)
		events[index] = domain.EventEnvelope{ID: "event-" + fmt.Sprint(sequence), Sequence: sequence, Type: "message", DeviceUUID: "device-1", OccurredAt: now}
	}
	repository := &fakeRepository{listedEvents: events}
	service := &syncService{router: &router{repository: repository, config: Config{Now: func() time.Time { return now }}}}
	stream := &scriptedWatchEventsStream{
		first:     &communicationv1.WatchEventsRequest{},
		requests:  make(chan *communicationv1.WatchEventsRequest, 2),
		responses: make(chan *communicationv1.WatchEventsResponse, 2),
	}
	finished := make(chan error, 1)
	go func() { finished <- service.watchEvents(context.Background(), stream) }()

	select {
	case response := <-stream.responses:
		if response.GetNextSequence() != 100 || len(response.GetEvents()) != 100 || response.GetEvents()[0].GetId() != "event-1" {
			t.Fatalf("unexpected first page: %+v", response)
		}
	case err := <-finished:
		t.Fatalf("stream ended before first page: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first event page")
	}

	select {
	case response := <-stream.responses:
		t.Fatalf("sent a second page before acknowledging the first: %+v", response)
	case <-time.After(2 * eventStreamPollInterval):
	}

	stream.requests <- &communicationv1.WatchEventsRequest{AcknowledgedSequence: 100}
	select {
	case response := <-stream.responses:
		if response.GetNextSequence() != 101 || len(response.GetEvents()) != 1 || response.GetEvents()[0].GetId() != "event-101" {
			t.Fatalf("unexpected second page: %+v", response)
		}
	case err := <-finished:
		t.Fatalf("stream ended before second page: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second event page")
	}

	stream.requests <- &communicationv1.WatchEventsRequest{AcknowledgedSequence: 101}
	close(stream.requests)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("watch events failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for stream to finish")
	}
}

func TestWatchEventsRejectsInvalidAcknowledgement(t *testing.T) {
	now := time.Now().UTC()
	repository := &fakeRepository{listedEvents: []domain.EventEnvelope{{
		ID: "event-1", Sequence: 1, Type: "message", DeviceUUID: "device-1", OccurredAt: now,
	}}}
	service := &syncService{router: &router{repository: repository, config: Config{Now: func() time.Time { return now }}}}
	stream := &scriptedWatchEventsStream{
		first:     &communicationv1.WatchEventsRequest{},
		requests:  make(chan *communicationv1.WatchEventsRequest, 1),
		responses: make(chan *communicationv1.WatchEventsResponse, 1),
	}
	stream.requests <- &communicationv1.WatchEventsRequest{AcknowledgedSequence: 2}

	err := service.watchEvents(context.Background(), stream)
	var connectError *connect.Error
	if !errors.As(err, &connectError) || connectError.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected invalid-argument acknowledgement error, got %v", err)
	}
}
