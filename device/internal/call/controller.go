package call

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"sim-deck-device/internal/at"
)

var phoneNumberPattern = regexp.MustCompile(`^\+?[0-9]{1,20}$`)
var incomingNumberPattern = regexp.MustCompile(`^\+(?:CLIP|CCWA):\s*"(\+?[0-9]{1,20})"`)

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

// Snapshot represents simultaneous active, held, and waiting cellular call legs.
type Snapshot struct {
	State          State
	Active         bool
	Held           bool
	Waiting        bool
	IncomingNumber string
}

// Commander is the AT command capability required by the call controller.
type Commander interface {
	Send(context.Context, string) (at.Response, error)
}

// Controller maps application call actions to Quectel AT commands.
type Controller struct {
	commander   Commander
	snapshot    Snapshot
	events      chan Snapshot
	subscribers []chan Snapshot
	mu          sync.RWMutex
}

// NewController creates an idle call controller.
func NewController(commander Commander) *Controller {
	return &Controller{
		commander: commander,
		snapshot:  Snapshot{State: StateIdle},
		events:    make(chan Snapshot, 32),
	}
}

// Events returns every accepted call-state transition in order.
func (controller *Controller) Events() <-chan Snapshot {
	return controller.events
}

// Subscribe returns an independent ordered stream of future call-state transitions.
func (controller *Controller) Subscribe() <-chan Snapshot {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	stream := make(chan Snapshot, 32)
	controller.subscribers = append(controller.subscribers, stream)
	return stream
}

// State returns the last state accepted by the controller.
func (controller *Controller) State() State {
	return controller.Snapshot().State
}

// Refresh queries the modem's authoritative call list and updates the local snapshot.
func (controller *Controller) Refresh(ctx context.Context) error {
	response, err := controller.commander.Send(ctx, "AT+CLCC")
	if err != nil {
		return fmt.Errorf("query modem call state: %w", err)
	}
	if !response.OK() {
		return fmt.Errorf("modem rejected call state query: %s", response.Terminal)
	}
	if err := controller.ApplyCLCC(response.Lines); err != nil {
		return fmt.Errorf("parse modem call state: %w", err)
	}
	return nil
}

// Snapshot returns all concurrently known call-leg states.
func (controller *Controller) Snapshot() Snapshot {
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	return controller.snapshot
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
	controller.setSnapshot(Snapshot{State: StateDialing})
	return nil
}

// AddParticipant holds the current active call and dials a second call leg.
func (controller *Controller) AddParticipant(ctx context.Context, number string) error {
	if !phoneNumberPattern.MatchString(number) {
		return errors.New("phone number must contain only digits and an optional leading plus")
	}
	snapshot := controller.Snapshot()
	if !snapshot.Active || snapshot.Held || snapshot.Waiting {
		return fmt.Errorf("cannot add participant while call state is %s", snapshot.State)
	}
	if err := controller.command(ctx, "AT+CHLD=2"); err != nil {
		return err
	}
	if err := controller.command(ctx, "ATD"+number+";"); err != nil {
		_ = controller.command(ctx, "AT+CHLD=2")
		return err
	}
	snapshot.Active = false
	snapshot.Held = true
	snapshot.State = StateDialing
	controller.setSnapshot(snapshot)
	return nil
}

// Merge combines the active and held call legs into one conference call.
func (controller *Controller) Merge(ctx context.Context) error {
	snapshot := controller.Snapshot()
	if !snapshot.Active || !snapshot.Held {
		return fmt.Errorf("cannot merge while call state is %s", snapshot.State)
	}
	if err := controller.command(ctx, "AT+CHLD=3"); err != nil {
		return err
	}
	snapshot.Active = true
	snapshot.Held = false
	snapshot.Waiting = false
	snapshot.IncomingNumber = ""
	snapshot.State = StateActive
	controller.setSnapshot(snapshot)
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
	snapshot := controller.Snapshot()
	snapshot.State = StateActive
	snapshot.Active = true
	snapshot.Waiting = false
	controller.setSnapshot(snapshot)
	return nil
}

// Reject rejects an incoming call without affecting an unrelated held call.
func (controller *Controller) Reject(ctx context.Context) error {
	command := "ATH"
	snapshot := controller.Snapshot()
	if snapshot.Waiting {
		command = "AT+CHLD=0"
	}
	if err := controller.command(ctx, command); err != nil {
		return err
	}
	if snapshot.Waiting {
		snapshot.Waiting = false
		snapshot.IncomingNumber = ""
		snapshot.State = remainingState(snapshot)
		controller.setSnapshot(snapshot)
	} else {
		controller.setSnapshot(Snapshot{State: StateEnded})
	}
	return nil
}

// Hangup ends the current call.
func (controller *Controller) Hangup(ctx context.Context) error {
	if err := controller.command(ctx, "ATH"); err != nil {
		return err
	}
	controller.setSnapshot(Snapshot{State: StateEnded})
	return nil
}

// AnswerWaiting holds the active call and accepts the waiting call.
func (controller *Controller) AnswerWaiting(ctx context.Context) error {
	snapshot := controller.Snapshot()
	if !snapshot.Waiting {
		return fmt.Errorf("cannot answer waiting call while state is %s", controller.State())
	}
	if err := controller.command(ctx, "AT+CHLD=2"); err != nil {
		return err
	}
	if snapshot.Active {
		snapshot.Held = true
	}
	snapshot.Active = true
	snapshot.Waiting = false
	snapshot.State = StateActive
	controller.setSnapshot(snapshot)
	return nil
}

// Hold places the only active call on hold.
func (controller *Controller) Hold(ctx context.Context) error {
	snapshot := controller.Snapshot()
	if !snapshot.Active || snapshot.Held || snapshot.Waiting {
		return fmt.Errorf("cannot hold while call state is %s", snapshot.State)
	}
	if err := controller.command(ctx, "AT+CHLD=2"); err != nil {
		return err
	}
	snapshot.Active = false
	snapshot.Held = true
	snapshot.State = StateHeld
	controller.setSnapshot(snapshot)
	return nil
}

// Resume makes the only held call active again.
func (controller *Controller) Resume(ctx context.Context) error {
	snapshot := controller.Snapshot()
	if snapshot.Active || !snapshot.Held || snapshot.Waiting {
		return fmt.Errorf("cannot resume while call state is %s", snapshot.State)
	}
	if err := controller.command(ctx, "AT+CHLD=2"); err != nil {
		return err
	}
	snapshot.Active = true
	snapshot.Held = false
	snapshot.State = StateActive
	controller.setSnapshot(snapshot)
	return nil
}

// Swap toggles between the active and held calls.
func (controller *Controller) Swap(ctx context.Context) error {
	snapshot := controller.Snapshot()
	if !snapshot.Active || !snapshot.Held || snapshot.Waiting {
		return fmt.Errorf("cannot swap while call state is %s", snapshot.State)
	}
	return controller.command(ctx, "AT+CHLD=2")
}

// SendDTMF emits one telephone keypad tone.
func (controller *Controller) SendDTMF(ctx context.Context, digit string) error {
	if len(digit) != 1 || !strings.Contains("0123456789*#", digit) {
		return errors.New("DTMF digit must be 0-9, star, or hash")
	}
	if !controller.Snapshot().Active {
		return fmt.Errorf("cannot send DTMF while call state is %s", controller.State())
	}
	return controller.command(ctx, fmt.Sprintf("AT+VTS=\"%s\"", digit))
}

// ApplyURC updates call state from one unsolicited modem notification.
func (controller *Controller) ApplyURC(urc string) {
	snapshot := controller.Snapshot()
	switch {
	case urc == "RING":
		if snapshot.Active || snapshot.Held {
			snapshot.Waiting = true
			snapshot.State = StateWaiting
		} else {
			snapshot.State = StateRinging
		}
	case strings.HasPrefix(urc, "+CLIP:"):
		number, ok := incomingNumber(urc)
		if !ok {
			return
		}
		snapshot.IncomingNumber = number
		if snapshot.State == StateIdle || snapshot.State == StateEnded {
			snapshot.State = StateRinging
		}
	case strings.HasPrefix(urc, "+CCWA:"):
		snapshot.Waiting = true
		snapshot.State = StateWaiting
		if number, ok := incomingNumber(urc); ok {
			snapshot.IncomingNumber = number
		}
	case urc == "NO CARRIER", urc == "NO ANSWER", urc == "BUSY":
		if snapshot.Held {
			snapshot.Active = false
			snapshot.Waiting = false
			snapshot.State = StateHeld
		} else {
			snapshot = Snapshot{State: StateEnded}
		}
	default:
		return
	}
	controller.setSnapshot(snapshot)
}

// ApplyCLCC replaces the local snapshot with the modem's authoritative call list.
func (controller *Controller) ApplyCLCC(lines []string) error {
	previous := controller.Snapshot()
	snapshot := Snapshot{IncomingNumber: previous.IncomingNumber}
	dialing := false
	ringing := false
	callCount := 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "+CLCC:") {
			continue
		}
		fields := strings.Split(strings.TrimSpace(strings.TrimPrefix(line, "+CLCC:")), ",")
		if len(fields) < 5 {
			return fmt.Errorf("malformed CLCC response: %s", line)
		}
		status, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil || status < 0 || status > 5 {
			return fmt.Errorf("malformed CLCC call state: %s", line)
		}
		mode, err := strconv.Atoi(strings.TrimSpace(fields[3]))
		if err != nil {
			return fmt.Errorf("malformed CLCC call mode: %s", line)
		}
		if mode != 0 {
			continue
		}
		direction, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || (direction != 0 && direction != 1) {
			return fmt.Errorf("malformed CLCC call direction: %s", line)
		}
		callCount++
		switch status {
		case 0:
			snapshot.Active = true
		case 1:
			snapshot.Held = true
		case 2:
			dialing = true
		case 3, 4:
			ringing = true
		case 5:
			snapshot.Waiting = true
		}
		if direction == 1 && (status == 4 || status == 5) && len(fields) > 5 {
			number := strings.Trim(strings.TrimSpace(fields[5]), "\"")
			if incomingNumberValue(number) {
				snapshot.IncomingNumber = number
			}
		}
	}
	if callCount == 0 {
		state := controller.State()
		if state != StateIdle && state != StateEnded {
			controller.setSnapshot(Snapshot{State: StateEnded})
		}
		return nil
	}
	switch {
	case snapshot.Waiting:
		snapshot.State = StateWaiting
	case snapshot.Active:
		snapshot.State = StateActive
	case snapshot.Held:
		snapshot.State = StateHeld
	case ringing:
		snapshot.State = StateRinging
	case dialing:
		snapshot.State = StateDialing
	default:
		return errors.New("CLCC response contains no supported call state")
	}
	controller.setSnapshot(snapshot)
	return nil
}

func incomingNumberValue(value string) bool {
	if value == "" {
		return false
	}
	digitCount := 0
	for index, character := range value {
		if character == '+' {
			if index != 0 {
				return false
			}
			continue
		}
		if character < '0' || character > '9' {
			return false
		}
		digitCount++
	}
	return digitCount > 0
}

func incomingNumber(urc string) (string, bool) {
	match := incomingNumberPattern.FindStringSubmatch(strings.TrimSpace(urc))
	if len(match) != 2 {
		return "", false
	}
	return match[1], true
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

func (controller *Controller) setSnapshot(snapshot Snapshot) {
	controller.mu.Lock()
	if controller.snapshot == snapshot {
		controller.mu.Unlock()
		return
	}
	controller.snapshot = snapshot
	subscribers := append([]chan Snapshot(nil), controller.subscribers...)
	controller.mu.Unlock()
	controller.events <- snapshot
	for _, subscriber := range subscribers {
		subscriber <- snapshot
	}
}

func remainingState(snapshot Snapshot) State {
	if snapshot.Active {
		return StateActive
	}
	if snapshot.Held {
		return StateHeld
	}
	return StateEnded
}
