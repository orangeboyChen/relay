package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"

	"golang.org/x/net/websocket"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/domain"
)

var _ = Describe("Encrypted event WebSocket", func() {
	var (
		repository *fakeRepository
		server     *httptest.Server
	)

	BeforeEach(func() {
		repository = &fakeRepository{
			ownerID:         "owner-1",
			listEventsCalls: make(chan int64, 4),
			listedEvents: []domain.EventEnvelope{
				{ID: "01900000-0000-7000-8000-000000000011", Sequence: 42, Type: "message.received"},
				{ID: "01900000-0000-7000-8000-000000000012", Sequence: 43, Type: "call.state"},
			},
		}
		server = httptest.NewServer(Handler(repository, Config{}))
	})

	AfterEach(func() {
		server.Close()
	})

	connect := func(cursor string, token string) (*websocket.Conn, error) {
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/v1/events/ws?after_sequence=" + cursor
		config, err := websocket.NewConfig(url, "http://localhost")
		if err != nil {
			return nil, err
		}
		if token != "" {
			config.Header.Set("Authorization", "Bearer "+token)
		}
		return websocket.DialConfig(config)
	}

	It("streams opaque events after the cursor and advances only after an exact ACK", func() {
		connection, err := connect("41", "access-token")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)

		var delivery eventStreamDelivery
		Expect(websocket.JSON.Receive(connection, &delivery)).To(Succeed())
		Expect(delivery.Type).To(Equal("events"))
		Expect(delivery.Events).To(HaveLen(2))
		Expect(delivery.NextSequence).To(Equal(int64(43)))
		Expect(<-repository.listEventsCalls).To(Equal(int64(41)))

		repository.setListedEvents(nil)
		Expect(websocket.JSON.Send(connection, eventStreamACK{Type: "ack", Sequence: 43})).To(Succeed())
		Expect(<-repository.listEventsCalls).To(Equal(int64(43)))
	})

	It("rejects an invalid ACK without accepting the cursor", func() {
		connection, err := connect("41", "access-token")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)

		var delivery eventStreamDelivery
		Expect(websocket.JSON.Receive(connection, &delivery)).To(Succeed())
		Expect(<-repository.listEventsCalls).To(Equal(int64(41)))
		Expect(websocket.JSON.Send(connection, eventStreamACK{Type: "ack", Sequence: 42})).To(Succeed())

		var failure eventStreamError
		Expect(websocket.JSON.Receive(connection, &failure)).To(Succeed())
		Expect(failure).To(Equal(eventStreamError{Type: "error", Code: "invalid_ack"}))
		Expect(repository.listEventsCalls).To(BeEmpty())
	})

	It("reports repository failures without exposing details", func() {
		repository.listEventsError = context.DeadlineExceeded
		connection, err := connect("0", "access-token")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(connection.Close)

		var failure eventStreamError
		Expect(websocket.JSON.Receive(connection, &failure)).To(Succeed())
		Expect(failure).To(Equal(eventStreamError{Type: "error", Code: "storage_error"}))
	})

	It("requires owner authentication before upgrading", func() {
		_, err := connect("0", "")
		Expect(err).To(HaveOccurred())
	})

	It("rejects an invalid cursor before upgrading", func() {
		request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/events/ws?after_sequence=-1", nil)
		Expect(err).NotTo(HaveOccurred())
		request.Header.Set("Authorization", "Bearer access-token")

		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(response.Body.Close)
		Expect(response.StatusCode).To(Equal(http.StatusBadRequest))
		var body map[string]any
		Expect(json.NewDecoder(response.Body).Decode(&body)).To(Succeed())
		Expect(body).To(HaveKey("error"))
	})
})
