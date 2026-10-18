package push

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"sim-deck-server/internal/apns"
	"sim-deck-server/internal/store"
)

var ErrMissingVoIPToken = errors.New("installation has no active VoIP push token")

// Repository persists leased outbox work and APNs token invalidation.
type Repository interface {
	ClaimVoIPPushJob(context.Context, time.Time, time.Duration) (store.OutboxPushJob, error)
	CompletePushJob(context.Context, int64, int, time.Time) error
	DiscardPushJob(context.Context, int64, int, time.Time, string) error
	RetryPushJob(context.Context, int64, int, time.Time, string) error
	DeleteVoIPPushToken(context.Context, string, string, []byte) error
}

// Sender delivers one encrypted payload to APNs.
type Sender interface {
	SendVoIP(context.Context, string, []byte, []byte) error
}

type alertRepository interface {
	ClaimPushJob(context.Context, time.Time, time.Duration) (store.OutboxPushJob, error)
	DeletePushTokenForDelivery(context.Context, string, string, string, []byte) error
}

type alertSender interface {
	SendAlert(context.Context, string, []byte, []byte) error
}

// Worker leases and delivers APNs outbox jobs.
type Worker struct {
	repository     Repository
	sender         Sender
	pollInterval   time.Duration
	leaseDuration  time.Duration
	minimumBackoff time.Duration
	maximumBackoff time.Duration
	now            func() time.Time
}

// Config controls polling, leasing, and exponential retry timing.
type Config struct {
	PollInterval   time.Duration
	LeaseDuration  time.Duration
	MinimumBackoff time.Duration
	MaximumBackoff time.Duration
	Now            func() time.Time
}

// NewWorker creates a worker with safe delivery defaults.
func NewWorker(repository Repository, sender Sender, config Config) *Worker {
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 30 * time.Second
	}
	if config.MinimumBackoff <= 0 {
		config.MinimumBackoff = time.Second
	}
	if config.MaximumBackoff < config.MinimumBackoff {
		config.MaximumBackoff = 30 * time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Worker{
		repository:     repository,
		sender:         sender,
		pollInterval:   config.PollInterval,
		leaseDuration:  config.LeaseDuration,
		minimumBackoff: config.MinimumBackoff,
		maximumBackoff: config.MaximumBackoff,
		now:            config.Now,
	}
}

// Run processes available jobs until cancellation or a storage failure.
func (worker *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(worker.pollInterval)
	defer ticker.Stop()
	for {
		processed, err := worker.ProcessOne(ctx)
		if err != nil {
			return err
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// ProcessOne delivers at most one available job.
func (worker *Worker) ProcessOne(ctx context.Context) (bool, error) {
	now := worker.now().UTC()
	var job store.OutboxPushJob
	var err error
	if repository, ok := worker.repository.(alertRepository); ok {
		job, err = repository.ClaimPushJob(ctx, now, worker.leaseDuration)
	} else {
		job, err = worker.repository.ClaimVoIPPushJob(ctx, now, worker.leaseDuration)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim push job: %w", err)
	}
	if len(job.DeviceToken) == 0 || job.Environment == "" {
		return true, worker.retry(ctx, job, now, ErrMissingVoIPToken)
	}
	if job.Kind == "alert.incoming_message" {
		sender, ok := worker.sender.(alertSender)
		if !ok {
			return true, worker.retry(ctx, job, now, errors.New("alert sender is not configured"))
		}
		err = sender.SendAlert(ctx, job.Environment, job.DeviceToken, job.Payload)
	} else {
		err = worker.sender.SendVoIP(ctx, job.Environment, job.DeviceToken, job.Payload)
	}
	if err == nil {
		return true, worker.repository.CompletePushJob(ctx, job.ID, job.Attempt, now)
	}

	var responseError *apns.ResponseError
	if errors.As(err, &responseError) {
		if responseError.InvalidatesToken() {
			var deleteErr error
			if repository, ok := worker.repository.(alertRepository); ok {
				deleteErr = repository.DeletePushTokenForDelivery(ctx, job.RoutingKey, job.Environment, pushTokenKind(job.Kind), job.DeviceToken)
			} else if pushTokenKind(job.Kind) == "voip" {
				deleteErr = worker.repository.DeleteVoIPPushToken(ctx, job.RoutingKey, job.Environment, job.DeviceToken)
			}
			if deleteErr != nil {
				return true, deleteErr
			}
		}
		if !responseError.Retryable() {
			return true, worker.repository.DiscardPushJob(ctx, job.ID, job.Attempt, now, boundedError(err))
		}
	}
	if errors.Is(err, apns.ErrInvalidPayload) {
		return true, worker.repository.DiscardPushJob(ctx, job.ID, job.Attempt, now, boundedError(err))
	}
	return true, worker.retry(ctx, job, now, err)
}

func pushTokenKind(jobKind string) string {
	if jobKind == "alert.incoming_message" {
		return "alert"
	}
	return "voip"
}

func (worker *Worker) retry(ctx context.Context, job store.OutboxPushJob, now time.Time, deliveryError error) error {
	delay := worker.minimumBackoff
	for attempt := 1; attempt < job.Attempt && delay < worker.maximumBackoff; attempt++ {
		delay *= 2
		if delay > worker.maximumBackoff {
			delay = worker.maximumBackoff
		}
	}
	return worker.repository.RetryPushJob(
		ctx,
		job.ID,
		job.Attempt,
		now.Add(delay),
		boundedError(deliveryError),
	)
}

func boundedError(err error) string {
	message := err.Error()
	if len(message) > 512 {
		return message[:512]
	}
	return message
}
