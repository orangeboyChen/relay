package api

import (
	"net/http"
	"time"

	"golang.org/x/net/websocket"

	"sim-deck-server/internal/domain"
)

const (
	eventStreamPollInterval = 250 * time.Millisecond
	eventStreamWriteTimeout = 10 * time.Second
	eventStreamACKTimeout   = 30 * time.Second
)

type eventStreamDelivery struct {
	Type         string                 `json:"type"`
	Events       []domain.EventEnvelope `json:"events"`
	NextSequence int64                  `json:"next_sequence"`
}

type eventStreamACK struct {
	Type     string `json:"type"`
	Sequence int64  `json:"sequence"`
}

type eventStreamError struct {
	Type string `json:"type"`
	Code string `json:"code"`
}

func (router *router) streamEvents(writer http.ResponseWriter, request *http.Request) {
	afterSequence, err := queryInt64(request, "after_sequence", 0)
	if err != nil || afterSequence < 0 {
		writeError(writer, http.StatusBadRequest, "invalid_cursor", "Event cursor must be a non-negative integer.")
		return
	}

	server := websocket.Server{
		Handler: func(connection *websocket.Conn) {
			router.serveEventStream(request, connection, afterSequence)
		},
		// Native apps authenticate with an access token and do not send a browser Origin.
		Handshake: func(*websocket.Config, *http.Request) error { return nil },
	}
	server.ServeHTTP(writer, request)
}

func (router *router) serveEventStream(
	request *http.Request,
	connection *websocket.Conn,
	afterSequence int64,
) {
	cursor := afterSequence
	for {
		events, err := router.repository.ListEvents(
			request.Context(), cursor, defaultEventPageSize, router.now(),
		)
		if err != nil {
			router.sendEventStreamError(connection, "storage_error")
			return
		}
		if len(events) == 0 {
			timer := time.NewTimer(eventStreamPollInterval)
			select {
			case <-request.Context().Done():
				timer.Stop()
				return
			case <-timer.C:
				continue
			}
		}

		nextSequence := events[len(events)-1].Sequence
		_ = connection.SetWriteDeadline(time.Now().Add(eventStreamWriteTimeout))
		if err := websocket.JSON.Send(connection, eventStreamDelivery{
			Type:         "events",
			Events:       events,
			NextSequence: nextSequence,
		}); err != nil {
			return
		}

		_ = connection.SetReadDeadline(time.Now().Add(eventStreamACKTimeout))
		var acknowledgement eventStreamACK
		if err := websocket.JSON.Receive(connection, &acknowledgement); err != nil {
			return
		}
		if acknowledgement.Type != "ack" || acknowledgement.Sequence != nextSequence {
			router.sendEventStreamError(connection, "invalid_ack")
			return
		}
		cursor = acknowledgement.Sequence
	}
}

func (router *router) sendEventStreamError(connection *websocket.Conn, code string) {
	_ = connection.SetWriteDeadline(time.Now().Add(eventStreamWriteTimeout))
	_ = websocket.JSON.Send(connection, eventStreamError{Type: "error", Code: code})
}
