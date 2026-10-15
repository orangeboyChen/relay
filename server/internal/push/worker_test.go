package push

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-server/internal/apns"
	"sim-deck-server/internal/store"
)

var _ = Describe("Push worker", func() {
	var (
		now        time.Time
		repository *memoryRepository
		sender     *memorySender
		worker     *Worker
	)

	BeforeEach(func() {
		now = time.Date(2026, 10, 16, 5, 0, 0, 0, time.UTC)
		repository = &memoryRepository{job: store.OutboxPushJob{
			ID:          7,
			Kind:        "voip.incoming_call",
			RoutingKey:  "01900000-0000-7000-8000-000000000101",
			Payload:     []byte(`{"ciphertext":"opaque"}`),
			Attempt:     1,
			Environment: "sandbox",
			DeviceToken: []byte{1, 2, 3},
		}}
		sender = &memorySender{}
		worker = NewWorker(repository, sender, Config{
			PollInterval:   time.Millisecond,
			LeaseDuration:  20 * time.Second,
			MinimumBackoff: 2 * time.Second,
			MaximumBackoff: 8 * time.Second,
			Now:            func() time.Time { return now },
		})
	})

	It("sends and completes an encrypted delivery", func() {
		processed, err := worker.ProcessOne(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(processed).To(BeTrue())
		Expect(repository.claimedAt).To(Equal(now))
		Expect(repository.leaseDuration).To(Equal(20 * time.Second))
		Expect(sender.environment).To(Equal("sandbox"))
		Expect(sender.token).To(Equal([]byte{1, 2, 3}))
		Expect(sender.payload).To(MatchJSON(`{"ciphertext":"opaque"}`))
		Expect(repository.completed).To(Equal(jobAction{id: 7, attempt: 1, at: now}))
	})

	It("backs off missing tokens and transient failures", func() {
		repository.job.DeviceToken = nil
		processed, err := worker.ProcessOne(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(processed).To(BeTrue())
		Expect(repository.retried.at).To(Equal(now.Add(2 * time.Second)))
		Expect(repository.retried.reason).To(ContainSubstring("no active VoIP"))

		repository.job.DeviceToken = []byte{1}
		repository.job.Attempt = 4
		sender.err = errors.New("network unavailable")
		_, err = worker.ProcessOne(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(repository.retried.at).To(Equal(now.Add(8 * time.Second)))
		Expect(repository.retried.reason).To(Equal("network unavailable"))
	})

	It("deletes rejected tokens and permanently discards their jobs", func() {
		sender.err = &apns.ResponseError{StatusCode: http.StatusGone, Reason: "Unregistered"}

		processed, err := worker.ProcessOne(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(processed).To(BeTrue())
		Expect(repository.deletedRoutingKey).To(Equal(repository.job.RoutingKey))
		Expect(repository.deletedEnvironment).To(Equal("sandbox"))
		Expect(repository.deletedToken).To(Equal([]byte{1, 2, 3}))
		Expect(repository.discarded.reason).To(ContainSubstring("Unregistered"))
	})

	It("discards permanent payload failures without deleting a valid token", func() {
		sender.err = apns.ErrInvalidPayload

		_, err := worker.ProcessOne(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(repository.discarded.reason).To(Equal(apns.ErrInvalidPayload.Error()))
		Expect(repository.deletedToken).To(BeNil())
	})

	It("returns idle and storage failures accurately", func() {
		repository.claimError = pgx.ErrNoRows
		processed, err := worker.ProcessOne(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(processed).To(BeFalse())

		repository.claimError = errors.New("database unavailable")
		processed, err = worker.ProcessOne(context.Background())
		Expect(err).To(MatchError("claim push job: database unavailable"))
		Expect(processed).To(BeFalse())
	})

	It("stops its polling loop when canceled", func() {
		repository.claimError = pgx.ErrNoRows
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		Expect(worker.Run(ctx)).To(Succeed())
	})

	It("applies bounded production defaults", func() {
		defaultWorker := NewWorker(repository, sender, Config{})

		Expect(defaultWorker.pollInterval).To(Equal(time.Second))
		Expect(defaultWorker.leaseDuration).To(Equal(30 * time.Second))
		Expect(defaultWorker.minimumBackoff).To(Equal(time.Second))
		Expect(defaultWorker.maximumBackoff).To(Equal(30 * time.Second))
		Expect(defaultWorker.now).NotTo(BeNil())
	})

	It("returns a polling storage failure", func() {
		repository.claimError = errors.New("database unavailable")

		Expect(worker.Run(context.Background())).To(MatchError("claim push job: database unavailable"))
	})

	It("bounds stored provider errors", func() {
		sender.err = errors.New(strings.Repeat("x", 600))

		_, err := worker.ProcessOne(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(repository.retried.reason).To(HaveLen(512))
	})

	It("returns token deletion failures without completing the job", func() {
		sender.err = &apns.ResponseError{StatusCode: http.StatusGone, Reason: "Unregistered"}
		repository.deleteError = errors.New("delete failed")

		processed, err := worker.ProcessOne(context.Background())

		Expect(processed).To(BeTrue())
		Expect(err).To(MatchError("delete failed"))
		Expect(repository.discarded).To(Equal(jobAction{}))
	})
})

type memorySender struct {
	environment string
	token       []byte
	payload     []byte
	err         error
}

func (sender *memorySender) SendVoIP(_ context.Context, environment string, token []byte, payload []byte) error {
	sender.environment = environment
	sender.token = append([]byte(nil), token...)
	sender.payload = append([]byte(nil), payload...)
	return sender.err
}

type jobAction struct {
	id      int64
	attempt int
	at      time.Time
	reason  string
}

type memoryRepository struct {
	job                store.OutboxPushJob
	claimError         error
	claimedAt          time.Time
	leaseDuration      time.Duration
	completed          jobAction
	discarded          jobAction
	retried            jobAction
	deletedRoutingKey  string
	deletedEnvironment string
	deletedToken       []byte
	deleteError        error
}

func (repository *memoryRepository) ClaimVoIPPushJob(_ context.Context, now time.Time, lease time.Duration) (store.OutboxPushJob, error) {
	repository.claimedAt = now
	repository.leaseDuration = lease
	return repository.job, repository.claimError
}

func (repository *memoryRepository) CompletePushJob(_ context.Context, id int64, attempt int, now time.Time) error {
	repository.completed = jobAction{id: id, attempt: attempt, at: now}
	return nil
}

func (repository *memoryRepository) DiscardPushJob(_ context.Context, id int64, attempt int, now time.Time, reason string) error {
	repository.discarded = jobAction{id: id, attempt: attempt, at: now, reason: reason}
	return nil
}

func (repository *memoryRepository) RetryPushJob(_ context.Context, id int64, attempt int, availableAt time.Time, reason string) error {
	repository.retried = jobAction{id: id, attempt: attempt, at: availableAt, reason: reason}
	return nil
}

func (repository *memoryRepository) DeleteVoIPPushToken(_ context.Context, routingKey string, environment string, token []byte) error {
	repository.deletedRoutingKey = routingKey
	repository.deletedEnvironment = environment
	repository.deletedToken = append([]byte(nil), token...)
	return repository.deleteError
}
