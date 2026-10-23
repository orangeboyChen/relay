package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

	It("writes a ciphertext snapshot using the expected revision", func() {
		response := request("PutHistorySnapshot", "Bearer owner-token",
			`{"kind":"HISTORY_KIND_CALLS","expectedRevision":"4","ciphertext":"AQID"}`)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(ContainSubstring(`"revision":"5"`))
		Expect(repository.historyWriteExpected).To(Equal(int64(4)))
		Expect(repository.historyWriteCiphertext).To(Equal([]byte{1, 2, 3}))
		Expect(repository.historyWriteAt).To(Equal(now))
	})

	It("maps concurrent snapshot writes to an aborted RPC", func() {
		repository.historyWriteError = domain.ErrHistoryRevisionConflict

		response := request("PutHistorySnapshot", "Bearer owner-token",
			`{"kind":"HISTORY_KIND_MESSAGES","expectedRevision":"2","ciphertext":"AQID"}`)

		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(ContainSubstring("aborted"))
	})

	It("does not expose repository errors for snapshot reads", func() {
		repository.historySnapshotError = errors.New("private storage detail")

		response := request("GetHistorySnapshot", "Bearer owner-token", `{"kind":"HISTORY_KIND_MESSAGES"}`)

		Expect(response.Code).To(Equal(http.StatusInternalServerError))
		Expect(response.Body.String()).NotTo(ContainSubstring("private storage detail"))
	})
})
