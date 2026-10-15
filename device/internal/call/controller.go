package call

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"sim-deck-device/internal/at"
)

var phoneNumberPattern = regexp.MustCompile(`^\+?[0-9]{1,20}$`)

// State is the locally known cellular call state.
type State string

const (
	StateIdle    State = "idle"
	StateDialing State = "dialing"
	StateRinging State = "ringing"
	StateActive  State = "active"
	StateHeld    State = "held"
	StateWaiting State = "waiting"
	StateEnded   State = "ended"
)

// Commander is the AT command capability required by the call controller.
type Commander interface {
	Send(context.Context, string) (at.Response, error)
}

// Controller maps application call actions to Quectel AT commands.
type Controller struct {
	commander Commander
	state     State
	mu        sync.RWMutex
}

// NewController creates an idle call controller.
func NewController(commander Commander) *Controller {
	return &Controller{commander: commander, state: StateIdle}
}

// State returns the last state accepted by the controller.
func (controller *Controller) State() State {
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	return controller.state
}

// Dial starts an outgoing voice call.
func (controller *Controller) Dial(ctx context.Context, number string) error {
	if !phoneNumberPattern.MatchString(number) {
		return errors.New("phone number must contain only digits and an optional leading plus")
	}
	if controller.State() != StateIdle && controller.State() != StateEnded {
		return fmt.Errorf("cannot dial while call state is %s", controller.State())
	}
	if err := controller.command(ctx, "ATD"+number+";"); err != nil {
		return err
	}
	controller.setState(StateDialing)
	return nil
}

// Answer accepts the currently ringing call.
func (controller *Controller) Answer(ctx context.Context) error {
	if state := controller.State(); state != StateRinging {
		return fmt.Errorf("cannot answer while call state is %s", state)
	}
	if err := controller.command(ctx, "ATA"); err != nil {
		return err
	}
	controller.setState(StateActive)
	return nil
}

// Reject rejects an incoming call without affecting an unrelated held call.
func (controller *Controller) Reject(ctx context.Context) error {
	command := "ATH"
	if controller.State() == StateWaiting {
		command = "AT+CHLD=0"
	}
	if err := controller.command(ctx, command); err != nil {
		return err
	}
	controller.setState(StateEnded)
	return nil
}

// Hangup ends the current call.
func (controller *Controller) Hangup(ctx context.Context) error {
	if err := controller.command(ctx, "ATH"); err != nil {
		return err
	}
	controller.setState(StateEnded)
	return nil
}

// AnswerWaiting holds the active call and accepts the waiting call.
func (controller *Controller) AnswerWaiting(ctx context.Context) error {
	if controller.State() != StateWaiting {
		return fmt.Errorf("cannot answer waiting call while state is %s", controller.State())
	}
	if err := controller.command(ctx, "AT+CHLD=2"); err != nil {
		return err
	}
	controller.setState(StateActive)
	return nil
}

// Swap toggles between the active and held calls.
func (controller *Controller) Swap(ctx context.Context) error {
	if state := controller.State(); state != StateActive && state != StateHeld {
		return fmt.Errorf("cannot swap while call state is %s", state)
	}
	return controller.command(ctx, "AT+CHLD=2")
}

// SendDTMF emits one telephone keypad tone.
func (controller *Controller) SendDTMF(ctx context.Context, digit string) error {
	if len(digit) != 1 || !strings.Contains("0123456789*#", digit) {
		return errors.New("DTMF digit must be 0-9, star, or hash")
	}
	if controller.State() != StateActive {
		return fmt.Errorf("cannot send DTMF while call state is %s", controller.State())
	}
	return controller.command(ctx, fmt.Sprintf("AT+VTS=\"%s\"", digit))
}

// ApplyURC updates call state from one unsolicited modem notification.
func (controller *Controller) ApplyURC(urc string) {
	switch {
	case urc == "RING":
		if controller.State() == StateActive || controller.State() == StateHeld {
			controller.setState(StateWaiting)
		} else {
			controller.setState(StateRinging)
		}
	case strings.HasPrefix(urc, "+CCWA:"):
		controller.setState(StateWaiting)
	case urc == "NO CARRIER", urc == "NO ANSWER", urc == "BUSY":
		controller.setState(StateEnded)
	}
}

func (controller *Controller) command(ctx context.Context, command string) error {
	response, err := controller.commander.Send(ctx, command)
	if err != nil {
		return err
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected %s: %s", command, response.Terminal)
	}
	return nil
}

func (controller *Controller) setState(state State) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	controller.state = state
}
