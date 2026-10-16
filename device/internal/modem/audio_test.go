package modem

import (
	"bufio"
	"context"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/at"
)

func newAudioTestClient(responses ...string) (*at.Client, <-chan string, func()) {
	clientTransport, modemTransport := net.Pipe()
	client := at.NewClient(clientTransport)
	commands := make(chan string, len(responses))
	go func() {
		reader := bufio.NewReader(modemTransport)
		for _, response := range responses {
			command, _ := reader.ReadString('\r')
			commands <- strings.TrimSuffix(command, "\r")
			_, _ = modemTransport.Write([]byte(response))
		}
	}()
	return client, commands, func() {
		_ = client.Close()
		_ = modemTransport.Close()
	}
}

var _ = Describe("Modem UAC configuration", func() {
	It("verifies the USB composition and enables volatile UAC voice routing", func() {
		client, commands, closeClient := newAudioTestClient(
			`+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,1`+"\r\nOK\r\n",
			"OK\r\n",
			"+QPCMV: 1,2\r\nOK\r\n",
		)
		DeferCleanup(closeClient)

		Expect(ConfigureUAC(context.Background(), client, time.Second)).To(Succeed())
		Expect(commands).To(Receive(Equal(usbCompositionQuery)))
		Expect(commands).To(Receive(Equal(uacEnableCommand)))
		Expect(commands).To(Receive(Equal(uacStatusQuery)))
	})

	It("rejects a composition with the UAC function disabled", func() {
		client, commands, closeClient := newAudioTestClient(
			`+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,0` + "\r\nOK\r\n",
		)
		DeferCleanup(closeClient)

		err := ConfigureUAC(context.Background(), client, time.Second)

		Expect(err).To(MatchError(ContainSubstring("USB audio interface is disabled")))
		Expect(commands).To(Receive(Equal(usbCompositionQuery)))
	})

	DescribeTable("rejects invalid USB composition responses",
		func(response string, expected string) {
			client, _, closeClient := newAudioTestClient(response)
			DeferCleanup(closeClient)

			Expect(ConfigureUAC(context.Background(), client, time.Second)).To(MatchError(expected))
		},
		Entry("when the query is rejected", "ERROR\r\n", "modem rejected USB composition query: ERROR"),
		Entry("when the UAC flag is missing", `+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0`+"\r\nOK\r\n", "modem USB composition response is missing the UAC function flag"),
		Entry("when no composition line is returned", "OK\r\n", "modem USB composition response is missing the UAC function flag"),
	)

	DescribeTable("rejects failed UAC voice activation",
		func(enableResponse string, statusResponse string, expected string) {
			responses := []string{`+QCFG: "usbcfg",0x2C7C,0x0125,1,1,1,1,1,0,1` + "\r\nOK\r\n", enableResponse}
			if statusResponse != "" {
				responses = append(responses, statusResponse)
			}
			client, _, closeClient := newAudioTestClient(responses...)
			DeferCleanup(closeClient)

			Expect(ConfigureUAC(context.Background(), client, time.Second)).To(MatchError(expected))
		},
		Entry("when enable is rejected", "ERROR\r\n", "", "modem rejected UAC voice routing: ERROR"),
		Entry("when status is rejected", "OK\r\n", "ERROR\r\n", "modem rejected UAC voice routing query: ERROR"),
		Entry("when the enabled mode is not confirmed", "OK\r\n", "+QPCMV: 1,0\r\nOK\r\n", "modem did not confirm UAC voice routing mode 1,2"),
	)

	It("validates dependencies and reports command timeouts", func() {
		Expect(ConfigureUAC(context.Background(), nil, time.Second)).To(MatchError("AT client and positive timeout are required for UAC configuration"))

		client, _, closeClient := newAudioTestClient()
		DeferCleanup(closeClient)
		Expect(ConfigureUAC(context.Background(), client, 0)).To(MatchError("AT client and positive timeout are required for UAC configuration"))

		client, _, closeClient = newAudioTestClient("ignored")
		DeferCleanup(closeClient)
		err := ConfigureUAC(context.Background(), client, time.Millisecond)
		Expect(err).To(MatchError(ContainSubstring("query USB composition: context deadline exceeded")))
	})
})
