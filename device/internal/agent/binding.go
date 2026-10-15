package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"sim-deck-device/internal/identity"
	"sim-deck-device/internal/serverapi"
)

// PairingClient is the narrow server operation required during first boot.
type PairingClient interface {
	Pair(context.Context, string, string, identity.State) (serverapi.PairingResult, error)
}

// EnsureBinding returns a bound identity, retrying with the same durable credential after interrupted pairing.
func EnsureBinding(ctx context.Context, config Config, store identity.Store, state identity.State, client PairingClient) (identity.State, error) {
	serverURL := strings.TrimRight(config.ServerURL, "/")
	if state.Credential != "" {
		if state.ServerURL != serverURL {
			return identity.State{}, errors.New("configured server does not match the server bound to this device identity")
		}
		return state, nil
	}
	prepared, err := store.PreparePairing(state)
	if err != nil {
		return identity.State{}, fmt.Errorf("prepare device pairing: %w", err)
	}
	pairingToken, err := ReadSecret(config.PairingTokenFile)
	if err != nil {
		return identity.State{}, err
	}
	result, err := client.Pair(ctx, pairingToken, prepared.PendingCredential, prepared)
	if err != nil {
		return identity.State{}, err
	}
	bound, err := store.Bind(prepared, serverURL, result.NumericID)
	if err != nil {
		return identity.State{}, fmt.Errorf("persist device binding: %w", err)
	}
	return bound, nil
}
