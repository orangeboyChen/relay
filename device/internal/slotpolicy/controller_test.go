package slotpolicy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/at"
)

type fakeCommander struct {
	commands []string
	response at.Response
	err      error
}

func (commander *fakeCommander) Send(_ context.Context, command string) (at.Response, error) {
	commander.commands = append(commander.commands, command)
	return commander.response, commander.err
}

var _ = Describe("Slot policy controller", func() {
	var commander *fakeCommander
	var directory string

	BeforeEach(func() {
		commander = &fakeCommander{response: at.Response{Terminal: "OK"}}
		directory = GinkgoT().TempDir()
	})

	It("defaults every service on while leaving airplane mode off", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Current()).To(Equal(Settings{
			DataEnabled: true, IncomingCallsEnabled: true, IncomingSMSEnabled: true,
		}))
	})

	It("persists incoming service policy without sending radio commands", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		disabled := false
		settings, err := controller.Configure(context.Background(), Update{
			IncomingCallsEnabled: &disabled,
			IncomingSMSEnabled:   &disabled,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.IncomingCallsEnabled).To(BeFalse())
		Expect(settings.IncomingSMSEnabled).To(BeFalse())
		Expect(commander.commands).To(BeEmpty())

		reloaded, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Current()).To(Equal(settings))
		info, err := os.Stat(filepath.Join(directory, "slot-policy.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("applies airplane and data changes in modem-safe order", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		enabled, disabled := true, false
		_, err = controller.Configure(context.Background(), Update{AirplaneModeEnabled: &enabled})
		Expect(err).NotTo(HaveOccurred())
		_, err = controller.Configure(context.Background(), Update{DataEnabled: &disabled})
		Expect(err).NotTo(HaveOccurred())
		_, err = controller.Configure(context.Background(), Update{AirplaneModeEnabled: &disabled})
		Expect(err).NotTo(HaveOccurred())
		Expect(commander.commands).To(Equal([]string{"AT+CFUN=0", "AT+CFUN=1", "AT+CGATT=0"}))
	})

	It("restores persisted radio state after modem initialization", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		disabled := false
		_, err = controller.Configure(context.Background(), Update{DataEnabled: &disabled})
		Expect(err).NotTo(HaveOccurred())
		commander.commands = nil
		Expect(controller.Restore(context.Background())).To(Succeed())
		Expect(commander.commands).To(Equal([]string{"AT+CFUN=1", "AT+CGATT=0"}))
	})

	It("does not persist a radio change rejected by the modem", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		commander.err = errors.New("serial unavailable")
		disabled := false
		_, err = controller.Configure(context.Background(), Update{DataEnabled: &disabled})
		Expect(err).To(MatchError(ContainSubstring("serial unavailable")))
		Expect(controller.Current().DataEnabled).To(BeTrue())
		_, statError := os.Stat(filepath.Join(directory, "slot-policy.json"))
		Expect(statError).To(MatchError(os.ErrNotExist))
	})

	It("validates constructor dependencies and stored state", func() {
		_, err := NewController("relative", commander, time.Second)
		Expect(err).To(HaveOccurred())
		_, err = NewController(directory, nil, time.Second)
		Expect(err).To(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, "slot-policy.json"), []byte(`{"version":99}`), 0o600)).To(Succeed())
		_, err = NewController(directory, commander, time.Second)
		Expect(err).To(MatchError("persisted slot policy is invalid"))
	})
})
