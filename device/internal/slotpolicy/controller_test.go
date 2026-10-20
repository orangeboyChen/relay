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

	It("persists VoLTE preference after the modem accepts the IMS setting", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		enabled := true

		settings, err := controller.Configure(context.Background(), Update{VoLTEEnabled: &enabled})

		Expect(err).NotTo(HaveOccurred())
		Expect(settings.VoLTEEnabled).To(BeTrue())
		Expect(commander.commands).To(Equal([]string{`AT+QCFG="ims",1`}))
		reloaded, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Current().VoLTEEnabled).To(BeTrue())
	})

	It("restarts the modem explicitly after an IMS setting change", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())

		Expect(controller.RestartModem(context.Background())).To(Succeed())

		Expect(commander.commands).To(Equal([]string{"AT+CFUN=1,1"}))
	})

	It("reports a modem restart failure to the caller", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		commander.err = errors.New("serial disconnected")

		Expect(controller.RestartModem(context.Background())).To(MatchError(ContainSubstring("serial disconnected")))
		Expect(commander.commands).To(Equal([]string{"AT+CFUN=1,1"}))
	})

	It("keeps the previous VoLTE preference when IMS is unsupported", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		commander.response = at.Response{Terminal: "+CME ERROR: 4"}
		enabled := true

		settings, err := controller.Configure(context.Background(), Update{VoLTEEnabled: &enabled})

		Expect(err).To(MatchError(`modem rejected slot policy command AT+QCFG="ims",1: +CME ERROR: 4`))
		Expect(settings.VoLTEEnabled).To(BeFalse())
		Expect(controller.Current().VoLTEEnabled).To(BeFalse())
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

	It("does not rewrite state or touch the modem for an unchanged policy", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		enabled := true

		settings, err := controller.Configure(context.Background(), Update{
			DataEnabled:          &enabled,
			IncomingCallsEnabled: &enabled,
			IncomingSMSEnabled:   &enabled,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(settings).To(Equal(controller.Current()))
		Expect(commander.commands).To(BeEmpty())
		_, statError := os.Stat(filepath.Join(directory, "slot-policy.json"))
		Expect(statError).To(MatchError(os.ErrNotExist))
	})

	It("surfaces a modem terminal rejection without changing the saved policy", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		commander.response = at.Response{Terminal: "ERROR"}
		disabled := false

		settings, err := controller.Configure(context.Background(), Update{DataEnabled: &disabled})

		Expect(err).To(MatchError("modem rejected slot policy command AT+CGATT=0: ERROR"))
		Expect(settings.DataEnabled).To(BeTrue())
		Expect(controller.Current().DataEnabled).To(BeTrue())
	})

	It("persists data preference changes while airplane mode keeps the radio disabled", func() {
		controller, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		enabled, disabled := true, false
		_, err = controller.Configure(context.Background(), Update{AirplaneModeEnabled: &enabled})
		Expect(err).NotTo(HaveOccurred())
		commander.commands = nil

		settings, err := controller.Configure(context.Background(), Update{DataEnabled: &disabled})

		Expect(err).NotTo(HaveOccurred())
		Expect(settings.DataEnabled).To(BeFalse())
		Expect(commander.commands).To(BeEmpty())
		reloaded, err := NewController(directory, commander, time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(reloaded.Current()).To(Equal(settings))
	})

	It("validates constructor dependencies and stored state", func() {
		_, err := NewController("relative", commander, time.Second)
		Expect(err).To(HaveOccurred())
		_, err = NewController(directory, nil, time.Second)
		Expect(err).To(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, "slot-policy.json"), []byte(`{"version":99}`), 0o600)).To(Succeed())
		_, err = NewController(directory, commander, time.Second)
		Expect(err).To(MatchError("persisted slot policy is invalid"))

		filePath := filepath.Join(GinkgoT().TempDir(), "not-a-directory")
		Expect(os.WriteFile(filePath, []byte("occupied"), 0o600)).To(Succeed())
		_, err = NewController(filePath, commander, time.Second)
		Expect(err).To(MatchError(ContainSubstring("create slot policy directory")))
	})
})
