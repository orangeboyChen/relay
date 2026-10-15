package call

import (
	"context"
	"testing"

	"sim-deck-device/internal/at"
)

type fakeCommander struct {
	commands []string
	response at.Response
}

func (fake *fakeCommander) Send(_ context.Context, command string) (at.Response, error) {
	fake.commands = append(fake.commands, command)
	return fake.response, nil
}

func TestDialAndHangup(t *testing.T) {
	commander := &fakeCommander{response: at.Response{Terminal: "OK"}}
	controller := NewController(commander)
	if err := controller.Dial(context.Background(), "+8613500000000"); err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if controller.State() != StateDialing {
		t.Fatalf("State() = %s", controller.State())
	}
	if err := controller.Hangup(context.Background()); err != nil {
		t.Fatalf("Hangup() error = %v", err)
	}
	if got := commander.commands; len(got) != 2 || got[0] != "ATD+8613500000000;" || got[1] != "ATH" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestIncomingAndWaitingCalls(t *testing.T) {
	commander := &fakeCommander{response: at.Response{Terminal: "OK"}}
	controller := NewController(commander)
	controller.ApplyURC("RING")
	if controller.State() != StateRinging {
		t.Fatalf("State() = %s, want ringing", controller.State())
	}
	if err := controller.Answer(context.Background()); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	controller.ApplyURC(`+CCWA: "10086",129,1`)
	if controller.State() != StateWaiting {
		t.Fatalf("State() = %s, want waiting", controller.State())
	}
	if err := controller.AnswerWaiting(context.Background()); err != nil {
		t.Fatalf("AnswerWaiting() error = %v", err)
	}
	if got := commander.commands; len(got) != 2 || got[0] != "ATA" || got[1] != "AT+CHLD=2" {
		t.Fatalf("commands = %#v", got)
	}
}

func TestSendDTMF(t *testing.T) {
	commander := &fakeCommander{response: at.Response{Terminal: "OK"}}
	controller := NewController(commander)
	controller.ApplyURC("RING")
	if err := controller.Answer(context.Background()); err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if err := controller.SendDTMF(context.Background(), "#"); err != nil {
		t.Fatalf("SendDTMF() error = %v", err)
	}
	if got := commander.commands[1]; got != `AT+VTS="#"` {
		t.Fatalf("DTMF command = %q", got)
	}
}
