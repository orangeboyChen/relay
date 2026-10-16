package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/serverapi"
)

// DiscoveryClient is the narrow server operation required during first boot.
type DiscoveryClient interface {
	SubmitDiscovery(context.Context, string, identity.State) (serverapi.DiscoveryResult, error)
	GetDiscoveryStatus(context.Context, string, string) (serverapi.DiscoveryResult, error)
}

// EnsureBinding waits for app approval and returns the durable server binding.
func EnsureBinding(ctx context.Context, config Config, store identity.Store, state identity.State, client DiscoveryClient) (identity.State, error) {
	serverURL := strings.TrimRight(config.ServerURL, "/")
	if state.Credential != "" {
		if state.ServerURL != serverURL {
			return identity.State{}, errors.New("configured server does not match the server bound to this device identity")
		}
		return state, nil
	}
	if config.DiscoveryPoll <= 0 {
		return identity.State{}, errors.New("device discovery poll interval must be positive")
	}
	prepared, err := store.PrepareDiscovery(state)
	if err != nil {
		return identity.State{}, fmt.Errorf("prepare device discovery: %w", err)
	}
	result, err := client.GetDiscoveryStatus(ctx, prepared.DeviceUUID, prepared.PendingCredential)
	if errors.Is(err, serverapi.ErrDiscoveryUnavailable) {
		result, err = client.SubmitDiscovery(ctx, prepared.PendingCredential, prepared)
	}
	if err != nil {
		return identity.State{}, err
	}

	ticker := time.NewTicker(config.DiscoveryPoll)
	defer ticker.Stop()
	for {
		switch result.Status {
		case serverapi.DiscoveryApproved:
			bound, bindError := store.Bind(prepared, serverURL, result.NumericID)
			if bindError != nil {
				return identity.State{}, fmt.Errorf("persist device binding: %w", bindError)
			}
			return bound, nil
		case serverapi.DiscoveryRejected:
			return identity.State{}, errors.New("device discovery was rejected by the owner")
		case serverapi.DiscoveryPending:
		case "":
			return identity.State{}, errors.New("server returned an empty device discovery state")
		default:
			return identity.State{}, fmt.Errorf("server returned unsupported device discovery state %q", result.Status)
		}

		select {
		case <-ctx.Done():
			return identity.State{}, ctx.Err()
		case <-ticker.C:
		}
		result, err = client.GetDiscoveryStatus(ctx, prepared.DeviceUUID, prepared.PendingCredential)
		if errors.Is(err, serverapi.ErrDiscoveryUnavailable) {
			return identity.State{}, errors.New("device discovery approval expired or became unavailable")
		}
		if err != nil {
			return identity.State{}, err
		}
	}
}
