package call

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/at"
)

type fakeCommander struct {
	commands []string
	response at.Response
	err      error
}

func (fake *fakeCommander) Send(_ context.Context, command string) (at.Response, error) {
	fake.commands = append(fake.commands, command)
	return fake.response, fake.err
}

var _ = Describe("Cellular call controller", func() {
	var (
		commander  *fakeCommander
		controller *Controller
	)

	BeforeEach(func() {
		commander = &fakeCommander{response: at.Response{Terminal: "OK"}}
		controller = NewController(commander)
	})

	It("dials and hangs up a valid number", func() {
		Expect(controller.Dial(context.Background(), "+8613500000000")).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateDialing}))
		Eventually(controller.Events()).Should(Receive(Equal(Snapshot{State: StateDialing})))
		Expect(controller.Hangup(context.Background())).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateEnded}))
		Eventually(controller.Events()).Should(Receive(Equal(Snapshot{State: StateEnded})))
		Expect(commander.commands).To(Equal([]string{"ATD+8613500000000;", "ATH"}))
	})

	DescribeTable("rejects invalid dial attempts without contacting the modem",
		func(number string) {
			Expect(controller.Dial(context.Background(), number)).To(MatchError(ContainSubstring("phone number")))
			Expect(commander.commands).To(BeEmpty())
		},
		Entry("empty", ""),
		Entry("letters", "12A"),
		Entry("embedded plus", "1+2"),
		Entry("too long", "+123456789012345678901"),
	)

	It("does not dial while another call is in progress", func() {
		controller.ApplyURC("RING")
		Expect(controller.Dial(context.Background(), "10086")).To(MatchError("cannot dial while call state is ringing"))
		Expect(commander.commands).To(BeEmpty())
	})

	It("answers an incoming call", func() {
		controller.ApplyURC("RING")
		controller.ApplyURC(`+CLIP: "+8613500000000",145,"",0,"",0`)
		Expect(controller.State()).To(Equal(StateRinging))
		Expect(controller.Snapshot().IncomingNumber).To(Equal("+8613500000000"))

		Expect(controller.Answer(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateActive, Active: true, IncomingNumber: "+8613500000000"}))
		Expect(commander.commands).To(Equal([]string{"ATA"}))
	})

	It("ignores malformed caller identification without losing the ringing state", func() {
		controller.ApplyURC("RING")
		controller.ApplyURC(`+CLIP: "private",128,"",0,"",0`)

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateRinging}))
	})

	It("preserves the active call when rejecting a waiting call", func() {
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())
		controller.ApplyURC(`+CCWA: "10086",129,1`)
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateWaiting, Active: true, Waiting: true, IncomingNumber: "10086"}))

		Expect(controller.Reject(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateActive, Active: true}))
		Expect(commander.commands).To(Equal([]string{"ATA", "AT+CHLD=0"}))
	})

	It("preserves a held call when rejecting a waiting call", func() {
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())
		Expect(controller.Hold(context.Background())).To(Succeed())
		controller.ApplyURC("RING")

		Expect(controller.Reject(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateHeld, Held: true}))
	})

	It("ends an orphan waiting indication when it is rejected", func() {
		controller.ApplyURC(`+CCWA: "10086",129,1`)

		Expect(controller.Reject(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateEnded}))
	})

	It("holds the active call when answering a waiting call", func() {
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())
		controller.ApplyURC(`+CCWA: "10086",129,1`)

		Expect(controller.AnswerWaiting(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateActive, Active: true, Held: true, IncomingNumber: "10086"}))
		Expect(commander.commands).To(Equal([]string{"ATA", "AT+CHLD=2"}))
	})

	It("allows DTMF on the active leg while another call waits", func() {
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())
		controller.ApplyURC(`+CCWA: "10010",129,1`)

		Expect(controller.SendDTMF(context.Background(), "#")).To(Succeed())

		Expect(commander.commands).To(HaveLen(2))
		Expect(commander.commands[1]).To(Equal(`AT+VTS="#"`))
	})

	It("holds and resumes a single active call", func() {
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())

		Expect(controller.Hold(context.Background())).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateHeld, Held: true}))
		Expect(controller.Resume(context.Background())).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateActive, Active: true}))
		Expect(commander.commands).To(Equal([]string{"ATA", "AT+CHLD=2", "AT+CHLD=2"}))
	})

	It("swaps only when active and held call legs both exist", func() {
		Expect(controller.Swap(context.Background())).To(MatchError("cannot swap while call state is idle"))
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())
		controller.ApplyURC(`+CCWA: "10010",129,1`)
		Expect(controller.AnswerWaiting(context.Background())).To(Succeed())

		Expect(controller.Swap(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateActive, Active: true, Held: true, IncomingNumber: "10010"}))
		Expect(commander.commands).To(HaveLen(3))
		Expect(commander.commands[2]).To(Equal("AT+CHLD=2"))
	})

	It("keeps a held leg when the foreground call ends", func() {
		controller.ApplyURC("RING")
		Expect(controller.Answer(context.Background())).To(Succeed())
		controller.ApplyURC(`+CCWA: "10010",129,1`)
		Expect(controller.AnswerWaiting(context.Background())).To(Succeed())

		controller.ApplyURC("NO CARRIER")

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateHeld, Held: true, IncomingNumber: "10010"}))
	})

	It("marks the call ended for terminal network notifications", func() {
		for _, notification := range []string{"NO CARRIER", "NO ANSWER", "BUSY"} {
			controller.ApplyURC("RING")
			controller.ApplyURC(notification)
			Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateEnded}))
		}
	})

	It("derives dialing, ringing, active, held, waiting, and ended states from CLCC", func() {
		Expect(controller.Dial(context.Background(), "10086")).To(Succeed())
		Eventually(controller.Events()).Should(Receive(Equal(Snapshot{State: StateDialing})))

		Expect(controller.ApplyCLCC([]string{`+CLCC: 1,0,3,0,0,"10086",129`})).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateRinging}))
		Expect(controller.ApplyCLCC([]string{`+CLCC: 1,0,0,0,0,"10086",129`})).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateActive, Active: true}))
		Expect(controller.ApplyCLCC([]string{
			`+CLCC: 1,0,1,0,0,"10086",129`,
			`+CLCC: 2,1,5,0,0,"10010",129`,
		})).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{
			State: StateWaiting, Held: true, Waiting: true, IncomingNumber: "10010",
		}))
		Expect(controller.ApplyCLCC(nil)).To(Succeed())
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateEnded}))
	})

	It("captures the incoming number from an authoritative CLCC poll", func() {
		controller := NewController(&fakeCommander{})

		Expect(controller.ApplyCLCC([]string{
			`+CLCC: 1,1,4,0,0,"+8613500000000",145`,
		})).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{
			State: StateRinging, IncomingNumber: "+8613500000000",
		}))
	})

	It("ignores an invalid incoming number in an authoritative CLCC poll", func() {
		controller := NewController(&fakeCommander{})

		Expect(controller.ApplyCLCC([]string{
			`+CLCC: 1,1,4,0,0,"+",145`,
		})).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateRinging}))
	})

	It("ignores the non-voice sessions reported alongside an EC20F call", func() {
		Expect(controller.Dial(context.Background(), "13500000000")).To(Succeed())

		Expect(controller.ApplyCLCC([]string{
			`+CLCC: 2,1,0,1,0,"",128`,
			`+CLCC: 1,1,0,1,0,"",128`,
			`+CLCC: 3,1,0,1,0,"",128`,
			`+CLCC: 4,0,3,0,0,"13500000000",129," B"`,
			`+CLCC: 5,1,0,1,0,"",128`,
		})).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateRinging}))
	})

	DescribeTable("rejects malformed CLCC state without changing the snapshot",
		func(line string) {
			Expect(controller.ApplyCLCC([]string{line})).To(HaveOccurred())
			Expect(controller.State()).To(Equal(StateIdle))
		},
		Entry("missing fields", "+CLCC: 1,0"),
		Entry("non-numeric state", "+CLCC: 1,0,nope,0,0"),
		Entry("unknown state", "+CLCC: 1,0,9,0,0"),
		Entry("non-numeric mode", "+CLCC: 1,0,0,voice,0"),
	)

	It("ignores unrelated CLCC response lines and suppresses duplicate events", func() {
		controller.ApplyURC("RING")
		Eventually(controller.Events()).Should(Receive(Equal(Snapshot{State: StateRinging})))
		controller.ApplyURC("RING")
		Consistently(controller.Events()).ShouldNot(Receive())
		Expect(controller.ApplyCLCC([]string{"vendor banner"})).To(Succeed())
		Expect(controller.State()).To(Equal(StateEnded))
	})

	It("broadcasts each transition to independent subscribers", func() {
		first := controller.Subscribe()
		second := controller.Subscribe()

		controller.ApplyURC("RING")

		Eventually(first).Should(Receive(Equal(Snapshot{State: StateRinging})))
		Eventually(second).Should(Receive(Equal(Snapshot{State: StateRinging})))
		Eventually(controller.Events()).Should(Receive(Equal(Snapshot{State: StateRinging})))
	})

	It("ignores unrelated notifications", func() {
		controller.ApplyURC("+CMTI: \"ME\",1")
		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateIdle}))
	})

	DescribeTable("rejects actions that do not match call state",
		func(action func() error, expected string) {
			Expect(action()).To(MatchError(ContainSubstring(expected)))
			Expect(commander.commands).To(BeEmpty())
		},
		Entry("answer", func() error { return controller.Answer(context.Background()) }, "cannot answer"),
		Entry("answer waiting", func() error { return controller.AnswerWaiting(context.Background()) }, "cannot answer waiting"),
		Entry("hold", func() error { return controller.Hold(context.Background()) }, "cannot hold"),
		Entry("resume", func() error { return controller.Resume(context.Background()) }, "cannot resume"),
		Entry("DTMF", func() error { return controller.SendDTMF(context.Background(), "1") }, "cannot send DTMF"),
	)

	DescribeTable("rejects invalid DTMF digits",
		func(digit string) {
			Expect(controller.SendDTMF(context.Background(), digit)).To(MatchError("DTMF digit must be 0-9, star, or hash"))
		},
		Entry("empty", ""),
		Entry("multiple", "12"),
		Entry("letter", "A"),
	)

	It("does not change state when the modem transport fails", func() {
		controller.ApplyURC("RING")
		commander.err = errors.New("serial disconnected")

		Expect(controller.Answer(context.Background())).To(MatchError("serial disconnected"))
		Expect(controller.State()).To(Equal(StateRinging))
	})

	It("does not mark a call ended when hangup fails", func() {
		controller.ApplyURC("RING")
		commander.err = errors.New("serial disconnected")

		Expect(controller.Hangup(context.Background())).To(MatchError("serial disconnected"))

		Expect(controller.State()).To(Equal(StateRinging))
	})

	It("allows a new dial after the prior call ended", func() {
		controller.ApplyURC("BUSY")

		Expect(controller.Dial(context.Background(), "10086")).To(Succeed())

		Expect(controller.State()).To(Equal(StateDialing))
	})

	It("returns a contextual error when the modem rejects a command", func() {
		controller.ApplyURC("RING")
		commander.response = at.Response{Terminal: "+CME ERROR: 3"}

		Expect(controller.Answer(context.Background())).To(MatchError("modem rejected ATA: +CME ERROR: 3"))
		Expect(controller.State()).To(Equal(StateRinging))
	})

	It("rejects a normal incoming call and clears all call legs", func() {
		controller.ApplyURC("RING")

		Expect(controller.Reject(context.Background())).To(Succeed())

		Expect(controller.Snapshot()).To(Equal(Snapshot{State: StateEnded}))
		Expect(commander.commands).To(Equal([]string{"ATH"}))
	})
})
