package radio

import (
	"context"
	"errors"
	"sync"
	"time"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/identity"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeCommander struct {
	responses map[string]at.Response
	err       error
}

func (commander *fakeCommander) Send(_ context.Context, command string) (at.Response, error) {
	if commander.err != nil {
		return at.Response{}, commander.err
	}
	return commander.responses[command], nil
}

type fakeRemote struct {
	mutex  sync.Mutex
	status []Status
	err    error
}

func (remote *fakeRemote) UpdateRadioStatus(_ context.Context, _ identity.State, status Status) error {
	remote.mutex.Lock()
	defer remote.mutex.Unlock()
	remote.status = append(remote.status, status)
	return remote.err
}

func (remote *fakeRemote) statuses() []Status {
	remote.mutex.Lock()
	defer remote.mutex.Unlock()
	return append([]Status(nil), remote.status...)
}

var _ = Describe("Radio status runtime", func() {
	state := identity.State{DeviceUUID: "01900000-0000-7000-8000-000000000001", Credential: "secret"}

	It("parses LTE access and four signal levels from EC20 responses", func() {
		commander := &fakeCommander{responses: map[string]at.Response{
			"AT+QNWINFO": {Lines: []string{`+QNWINFO: "FDD LTE","46000","LTE BAND 3",1850`}, Terminal: "OK"},
			"AT+CSQ":     {Lines: []string{"+CSQ: 17,99"}, Terminal: "OK"},
		}}
		remote := &fakeRemote{}
		runtime, err := NewRuntime(commander, remote, state, time.Hour, time.Second)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runtime.Run(ctx) }()

		Eventually(remote.statuses).Should(HaveLen(1))
		cancel()
		Eventually(done).Should(Receive(MatchError(context.Canceled)))
		Expect(remote.statuses()[0].RadioAccessTechnology).To(Equal("lte"))
		Expect(remote.statuses()[0].SignalBars).NotTo(BeNil())
		Expect(*remote.statuses()[0].SignalBars).To(Equal(3))
	})

	DescribeTable("normalizes network names",
		func(value string, expected string) {
			Expect(parseTechnology([]string{`+QNWINFO: "` + value + `","46000"`})).To(Equal(expected))
		},
		Entry("5G standalone", "NR5G-SA", "nr_sa"),
		Entry("5G non-standalone", "NR5G-NSA", "nr_nsa"),
		Entry("HSPA plus", "HSPA+", "hspa"),
		Entry("WCDMA", "WCDMA", "umts"),
		Entry("EDGE", "EDGE", "edge"),
		Entry("GSM", "GSM", "gsm"),
		Entry("no service", "No Service", "unknown"),
	)

	DescribeTable("maps CSQ RSSI to four bars",
		func(rssi string, expected *int) {
			actual := parseSignalBars([]string{"+CSQ: " + rssi + ",99"})
			if expected == nil {
				Expect(actual).To(BeNil())
				return
			}
			Expect(actual).NotTo(BeNil())
			Expect(*actual).To(Equal(*expected))
		},
		Entry("none", "0", intPointer(0)),
		Entry("one", "2", intPointer(1)),
		Entry("two", "10", intPointer(2)),
		Entry("three", "15", intPointer(3)),
		Entry("four", "20", intPointer(4)),
		Entry("unknown", "99", nil),
	)

	It("surfaces modem and upload failures", func() {
		_, err := NewRuntime(nil, &fakeRemote{}, state, time.Second, time.Second)
		Expect(err).To(HaveOccurred())

		commander := &fakeCommander{err: errors.New("serial failed")}
		runtime, err := NewRuntime(commander, &fakeRemote{}, state, time.Second, time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime.Run(context.Background())).To(MatchError(ContainSubstring("serial failed")))
	})
})

func intPointer(value int) *int { return &value }
