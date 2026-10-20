package slotpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sim-deck-device/internal/at"
)

const stateVersion = 1

// Settings is the effective owner policy for one physical modem slot.
type Settings struct {
	DataEnabled          bool `json:"data_enabled"`
	AirplaneModeEnabled  bool `json:"airplane_mode_enabled"`
	IncomingCallsEnabled bool `json:"incoming_calls_enabled"`
	IncomingSMSEnabled   bool `json:"incoming_sms_enabled"`
	VoLTEEnabled         bool `json:"volte_enabled"`
}

// Update contains optional policy changes from an authenticated encrypted command.
type Update struct {
	DataEnabled          *bool
	AirplaneModeEnabled  *bool
	IncomingCallsEnabled *bool
	IncomingSMSEnabled   *bool
	VoLTEEnabled         *bool
}

// Commander is the serialized AT command capability required by the policy controller.
type Commander interface {
	Send(context.Context, string) (at.Response, error)
}

// Controller persists slot policy and applies its radio-related settings to the modem.
type Controller struct {
	mu        sync.RWMutex
	path      string
	commander Commander
	timeout   time.Duration
	settings  Settings
}

type persistedSettings struct {
	Version int `json:"version"`
	Settings
}

// NewController opens the durable policy for one modem container.
func NewController(directory string, commander Commander, timeout time.Duration) (*Controller, error) {
	if directory == "" || !filepath.IsAbs(directory) || commander == nil || timeout <= 0 {
		return nil, errors.New("slot policy requires an absolute state directory, modem commander, and positive timeout")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create slot policy directory: %w", err)
	}
	path := filepath.Join(directory, "slot-policy.json")
	settings, err := load(path)
	if err != nil {
		return nil, err
	}
	return &Controller{path: path, commander: commander, timeout: timeout, settings: settings}, nil
}

// Current returns a consistent snapshot of the persisted owner policy.
func (controller *Controller) Current() Settings {
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	return controller.settings
}

// Restore reapplies the persisted radio policy after modem initialization.
func (controller *Controller) Restore(ctx context.Context) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.applyRadio(ctx, controller.settings, true, true)
}

// Configure applies a partial update and returns the resulting effective policy.
func (controller *Controller) Configure(ctx context.Context, update Update) (Settings, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	previous := controller.settings
	next := previous
	if update.DataEnabled != nil {
		next.DataEnabled = *update.DataEnabled
	}
	if update.AirplaneModeEnabled != nil {
		next.AirplaneModeEnabled = *update.AirplaneModeEnabled
	}
	if update.IncomingCallsEnabled != nil {
		next.IncomingCallsEnabled = *update.IncomingCallsEnabled
	}
	if update.IncomingSMSEnabled != nil {
		next.IncomingSMSEnabled = *update.IncomingSMSEnabled
	}
	if update.VoLTEEnabled != nil {
		next.VoLTEEnabled = *update.VoLTEEnabled
	}
	if previous.VoLTEEnabled != next.VoLTEEnabled {
		if err := controller.applyVoLTE(ctx, next.VoLTEEnabled); err != nil {
			return previous, err
		}
	}
	if err := controller.applyRadio(
		ctx,
		next,
		previous.AirplaneModeEnabled != next.AirplaneModeEnabled,
		previous.DataEnabled != next.DataEnabled,
	); err != nil {
		return previous, err
	}
	if next == previous {
		return next, nil
	}
	if err := save(controller.path, next); err != nil {
		return previous, err
	}
	controller.settings = next
	return next, nil
}

// RestartModem performs the full modem reset required after changing IMS configuration.
func (controller *Controller) RestartModem(ctx context.Context) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.send(ctx, "AT+CFUN=1,1")
}

func (controller *Controller) applyVoLTE(ctx context.Context, enabled bool) error {
	value := "0"
	if enabled {
		value = "1"
	}
	return controller.send(ctx, `AT+QCFG="ims",`+value)
}

func (controller *Controller) applyRadio(ctx context.Context, settings Settings, airplaneChanged bool, dataChanged bool) error {
	if settings.AirplaneModeEnabled {
		if airplaneChanged {
			return controller.send(ctx, "AT+CFUN=0")
		}
		return nil
	}
	if airplaneChanged {
		if err := controller.send(ctx, "AT+CFUN=1"); err != nil {
			return err
		}
		dataChanged = true
	}
	if dataChanged {
		value := "0"
		if settings.DataEnabled {
			value = "1"
		}
		return controller.send(ctx, "AT+CGATT="+value)
	}
	return nil
}

func (controller *Controller) send(ctx context.Context, command string) error {
	commandContext, cancel := context.WithTimeout(ctx, controller.timeout)
	defer cancel()
	response, err := controller.commander.Send(commandContext, command)
	if err != nil {
		return fmt.Errorf("apply slot policy command %s: %w", command, err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected slot policy command %s: %s", command, response.Terminal)
	}
	return nil
}

func load(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Settings{DataEnabled: true, IncomingCallsEnabled: true, IncomingSMSEnabled: true}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read slot policy: %w", err)
	}
	var persisted persistedSettings
	if err := json.Unmarshal(data, &persisted); err != nil || persisted.Version != stateVersion {
		return Settings{}, errors.New("persisted slot policy is invalid")
	}
	return persisted.Settings, nil
}

func save(path string, settings Settings) error {
	data, err := json.Marshal(persistedSettings{Version: stateVersion, Settings: settings})
	if err != nil {
		return fmt.Errorf("encode slot policy: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".slot-policy-*")
	if err != nil {
		return fmt.Errorf("create slot policy temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure slot policy temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write slot policy: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync slot policy: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close slot policy: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace slot policy: %w", err)
	}
	return nil
}
