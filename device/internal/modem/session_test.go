package modem

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"sim-deck-device/internal/at"
	"sim-deck-device/internal/call"
	"sim-deck-device/internal/command"
	"sim-deck-device/internal/sms"
)

const testDeliverPDU = "00040B913105000000F0000862015121436500044F60597D"

func newTestSession(sink MessageSink) (*Session, net.Conn, *at.Client) {
	clientTransport, modemTransport := net.Pipe()
	client := at.NewClient(clientTransport)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	session, err := NewSession(client, sms.NewInbox(time.Hour), sink, logger, 100*time.Millisecond)
	Expect(err).NotTo(HaveOccurred())
	return session, modemTransport, client
}

var _ = Describe("Modem session", func() {
	It("reads the EC20CE model and revision using standard read-only commands", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("ATI"))
			_, _ = modemTransport.Write([]byte("Quectel\r\nEC20CE\r\nRevision: EC20CEHCR06A08M1G\r\nOK\r\n"))
			command, _ = reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CGMM"))
			_, _ = modemTransport.Write([]byte("EC20CE\r\nOK\r\n"))
			command, _ = reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CGMR"))
			_, _ = modemTransport.Write([]byte("EC20CEHCR06A08M1G\r\nOK\r\n"))
		}()

		identity, err := ReadModemIdentity(context.Background(), client, time.Second)

		Expect(err).NotTo(HaveOccurred())
		Expect(identity.Manufacturer).To(Equal("Quectel"))
		Expect(identity.Model).To(Equal("EC20CE"))
		Expect(identity.Revision).To(Equal("EC20CEHCR06A08M1G"))
	})

	It("reads the modem equipment identity for local hardware derivation", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			command, _ := bufio.NewReader(modemTransport).ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CGSN"))
			_, _ = modemTransport.Write([]byte("867530900000001\r\nOK\r\n"))
		}()

		value, err := ReadEquipmentIdentity(context.Background(), client, time.Second)

		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal("867530900000001"))
	})

	It("falls back to the generic equipment identity command", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("867530900000002\r\nOK\r\n"))
		}()

		value, err := ReadEquipmentIdentity(context.Background(), client, time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(value).To(Equal("867530900000002"))
	})

	It("rejects equipment identity responses without a valid numeric value", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			for range 2 {
				_, _ = reader.ReadString('\r')
				_, _ = modemTransport.Write([]byte("+CGSN: invalid\r\nOK\r\n"))
			}
		}()

		_, err := ReadEquipmentIdentity(context.Background(), client, time.Second)
		Expect(err).To(MatchError("modem did not return a valid equipment identity"))
	})

	It("confirms the EC20/EC25 single physical slot from QSIMSTAT", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			command, _ := bufio.NewReader(modemTransport).ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+QSIMSTAT?"))
			_, _ = modemTransport.Write([]byte("+QSIMSTAT: 1,1\r\nOK\r\n"))
		}()

		indexes, err := ReadSIMSlotIndexes(context.Background(), client, time.Second)

		Expect(err).NotTo(HaveOccurred())
		Expect(indexes).To(Equal([]int{1}))
	})

	It("recognizes the physical slot even when insertion reporting is disabled", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			_, _ = bufio.NewReader(modemTransport).ReadString('\r')
			_, _ = modemTransport.Write([]byte("+QSIMSTAT: 0,0\r\nOK\r\n"))
		}()

		indexes, err := ReadSIMSlotIndexes(context.Background(), client, time.Second)

		Expect(err).NotTo(HaveOccurred())
		Expect(indexes).To(Equal([]int{1}))
	})

	It("rejects slot status responses without valid QSIMSTAT fields", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			_, _ = bufio.NewReader(modemTransport).ReadString('\r')
			_, _ = modemTransport.Write([]byte("+QSIMSTAT: bad,1\r\n+QSIMSTAT: 2,1\r\nOK\r\n"))
		}()

		_, err := ReadSIMSlotIndexes(context.Background(), client, time.Second)
		Expect(err).To(MatchError(ErrSIMSlotUnsupported))
	})

	It("reads physical SIM presence when QSIMSTAT is supported", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+QSIMSTAT?"))
			_, _ = modemTransport.Write([]byte("+QSIMSTAT: 1,1\r\nOK\r\n"))
		}()
		statuses, err := session.ReadSIMSlotStatus(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(statuses).To(Equal([]SIMSlotStatus{{Index: 1, Present: true}}))
	})

	It("fails safely when the modem does not expose slot status", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			_, _ = bufio.NewReader(modemTransport).ReadString('\r')
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
		}()
		_, err := session.ReadSIMSlotStatus(context.Background())
		Expect(err).To(MatchError(ErrSIMSlotUnsupported))
	})

	It("rejects malformed physical slot presence lines", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			_, _ = bufio.NewReader(modemTransport).ReadString('\r')
			_, _ = modemTransport.Write([]byte("+QSIMSTAT: bad,1\r\n+QSIMSTAT: 0,2\r\nOK\r\n"))
		}()
		_, err := session.ReadSIMSlotStatus(context.Background())
		Expect(err).To(MatchError(ErrSIMSlotUnsupported))
	})

	It("reads an ICCID from Quectel and generic modem commands", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+QCCID"))
			_, _ = modemTransport.Write([]byte("+QCCID: 8986000000000000000\r\nOK\r\n"))
		}()
		iccid, err := session.ReadSIMICCID(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(iccid).To(Equal("8986000000000000000"))
	})

	It("falls back to the generic ICCID command when the Quectel command is unavailable", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+QCCID"))
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
			command, _ = reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CCID"))
			_, _ = modemTransport.Write([]byte("+CCID: \"8986000000000000001\"\r\nOK\r\n"))
		}()

		iccid, err := session.ReadSIMICCID(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(iccid).To(Equal("8986000000000000001"))
	})

	It("rejects malformed ICCID responses from both supported commands", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			for range 2 {
				_, _ = reader.ReadString('\r')
				_, _ = modemTransport.Write([]byte("+CCID: 898600000000000000x\r\nOK\r\n"))
			}
		}()

		_, err := session.ReadSIMICCID(context.Background())

		Expect(err).To(MatchError("modem did not return a valid SIM ICCID"))
	})

	It("reads a provisioned subscriber number and network operator", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CNUM"))
			_, _ = modemTransport.Write([]byte("+CNUM: \"\",\"+8613500000000\",145\r\nOK\r\n"))
			command, _ = reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+COPS?"))
			_, _ = modemTransport.Write([]byte("+COPS: 0,0,\"CHINA MOBILE\",7\r\nOK\r\n"))
		}()

		profile, err := session.ReadSIMProfile(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(profile.PhoneNumber).To(Equal("+8613500000000"))
		Expect(profile.Operator).To(Equal("CHINA MOBILE"))
	})

	It("keeps available SIM profile fields when the other query is unsupported", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+COPS: 0,0,\"China Unicom\",7\r\nOK\r\n"))
		}()

		profile, err := session.ReadSIMProfile(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(profile.PhoneNumber).To(BeEmpty())
		Expect(profile.Operator).To(Equal("China Unicom"))
	})

	It("reports SIM profile query failures without leaking malformed values", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CNUM: \"\",\"not-a-number\",129\r\nOK\r\n"))
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+COPS: 0\r\nOK\r\n"))
		}()

		profile, err := session.ReadSIMProfile(context.Background())

		Expect(err).NotTo(HaveOccurred())
		Expect(profile).To(Equal(SIMProfile{}))
	})

	It("configures notifications and imports valid history around malformed entries", func() {
		received := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			received <- message
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		commands := make(chan string, len(initializationCommands)+3)
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+3; index++ {
				command, _ := reader.ReadString('\r')
				commands <- strings.TrimSuffix(command, "\r")
				switch command {
				case simReadinessCommand + "\r":
					_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
				case operatorSelectionQuery + "\r":
					_, _ = modemTransport.Write([]byte("+COPS: 0,0,\"CHINA MOBILE\",7\r\nOK\r\n"))
				case "AT+CMGL=4\r":
					_, _ = modemTransport.Write([]byte("+CMGL: 1,0,,23\r\n" + testDeliverPDU + "\r\n+CMGL: 2,0,,1\r\nnot-hex\r\nOK\r\n"))
				default:
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		Expect(session.Initialize(context.Background())).To(Succeed())

		expectedCommands := append([]string{simReadinessCommand}, initializationCommands...)
		expectedCommands = append(expectedCommands, operatorSelectionQuery)
		expectedCommands = append(expectedCommands, "AT+CMGL=4")
		for _, expected := range expectedCommands {
			Eventually(commands).Should(Receive(Equal(expected)))
		}
		Eventually(received).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("你好"))))
	})

	It("selects combined SMS storage and requests stored-message notifications", func() {
		Expect(initializationCommands).To(ContainElement(`AT+CPMS="MT","MT","MT"`))
		Expect(initializationCommands).To(ContainElement("AT+CNMI=2,1,0,1,0"))
		Expect(initializationCommands).NotTo(ContainElement("AT+CNMI=2,2,0,1,0"))
	})

	It("restores automatic operator selection when the modem is deregistered", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		commands := make(chan string, len(initializationCommands)+4)
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+4; index++ {
				command, _ := reader.ReadString('\r')
				commands <- strings.TrimSuffix(command, "\r")
				switch command {
				case simReadinessCommand + "\r":
					_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
				case operatorSelectionQuery + "\r":
					_, _ = modemTransport.Write([]byte("+COPS: 2\r\nOK\r\n"))
				default:
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		Expect(session.Initialize(context.Background())).To(Succeed())

		expectedCommands := append([]string{simReadinessCommand}, initializationCommands...)
		expectedCommands = append(expectedCommands, operatorSelectionQuery, "AT+COPS=0", "AT+CMGL=4")
		for _, expected := range expectedCommands {
			Eventually(commands).Should(Receive(Equal(expected)))
		}
	})

	DescribeTable("parses operator selection without exposing the operator name",
		func(line string, expectedMode int, expectedAccessTechnology int, expectedFound bool) {
			mode, accessTechnology, found := parseOperatorSelection([]string{line})
			Expect(mode).To(Equal(expectedMode))
			Expect(accessTechnology).To(Equal(expectedAccessTechnology))
			Expect(found).To(Equal(expectedFound))
		},
		Entry("automatic LTE", `+COPS: 0,0,"CHINA MOBILE",7`, 0, 7, true),
		Entry("deregistered", "+COPS: 2", 2, -1, true),
		Entry("malformed", "+COPS: invalid", 0, -1, false),
		Entry("missing", "+CREG: 0,2", 0, -1, false),
	)

	It("fails initialization when a required modem command is rejected", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CME ERROR: 3\r\n"))
		}()

		err := session.Initialize(context.Background())

		Expect(err).To(MatchError("modem rejected initialization command ATE0: +CME ERROR: 3"))
	})

	It("submits an outbound SMS PDU after the modem prompt", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		commands := make(chan string, 1)
		payloads := make(chan string, 1)
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			commands <- command
			_, _ = modemTransport.Write([]byte("\r\n> "))
			payload, _ := reader.ReadBytes(0x1a)
			payloads <- string(payload[:len(payload)-1])
			_, _ = modemTransport.Write([]byte("\r\n+CMGS: 99\r\nOK\r\n"))
		}()

		Expect(session.SendSMS(context.Background(), "+8613500000000", "hello", 7)).To(Succeed())
		Eventually(commands).Should(Receive(HavePrefix("AT+CMGS=")))
		Eventually(payloads).Should(Receive(HavePrefix("0011000D91683105000000F0")))
	})

	It("submits every segment of a multipart outbound SMS", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		commands := make(chan string, 2)
		payloads := make(chan string, 2)
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < 2; index++ {
				command, _ := reader.ReadString('\r')
				commands <- strings.TrimSuffix(command, "\r")
				_, _ = modemTransport.Write([]byte("\r\n> "))
				payload, _ := reader.ReadBytes(0x1a)
				payloads <- string(payload)
				_, _ = modemTransport.Write([]byte("\r\n+CMGS: 12\r\nOK\r\n"))
			}
		}()

		Expect(session.SendSMS(context.Background(), "13500000000", strings.Repeat("你", 71), 0x2a)).To(Succeed())
		Eventually(commands).Should(Receive(HavePrefix("AT+CMGS=")))
		Eventually(commands).Should(Receive(HavePrefix("AT+CMGS=")))
		for index := 0; index < 2; index++ {
			Eventually(payloads).Should(Receive(ContainSubstring(fmt.Sprintf("0500032A02%02X", index+1))))
		}
	})

	It("uploads and submits an image MMS with optional text", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		image := []byte{0xff, 0xd8, 0xff, 0xd9}
		commands := make(chan string, 11)
		uploads := make(chan []byte, 2)
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < 11; index++ {
				command, readErr := reader.ReadString('\r')
				if readErr != nil {
					return
				}
				command = strings.TrimSuffix(command, "\r")
				commands <- command
				switch {
				case strings.HasPrefix(command, "AT+QFUPL="):
					length := len(image)
					if strings.Contains(command, "outgoing.txt") {
						length = len("hello")
					}
					_, _ = modemTransport.Write([]byte("\r\nCONNECT\r\n"))
					payload := make([]byte, length)
					_, _ = io.ReadFull(reader, payload)
					uploads <- payload
					_, _ = modemTransport.Write([]byte(fmt.Sprintf("\r\n+QFUPL: %d,1234\r\nOK\r\n", length)))
				case command == "AT+QMMSEND=100":
					_, _ = modemTransport.Write([]byte("OK\r\n"))
					_ = session.handleURC(context.Background(), "+QMMSEND: 0,200")
				default:
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		err := session.SendMMS(context.Background(), "+8613500000000", "hello", command.MessageAttachment{
			Filename: "photo.jpg",
			MIMEType: "image/jpeg",
			Data:     image,
		})

		Expect(err).NotTo(HaveOccurred())
		expectedCommands := []string{
			"AT+QMMSEDIT=0",
			`AT+QMMSEDIT=1,1,"+8613500000000"`,
			`AT+QFUPL="RAM:sd_outgoing.jpg",4,300,1`,
			`AT+QMMSEDIT=5,1,"RAM:sd_outgoing.jpg"`,
			`AT+QMMSCFG="character","UTF8"`,
			`AT+QFUPL="RAM:sd_outgoing.txt",5,300,1`,
			`AT+QMMSEDIT=5,1,"RAM:sd_outgoing.txt"`,
			"AT+QMMSEND=100",
			"AT+QMMSEDIT=0",
			`AT+QFDEL="RAM:sd_outgoing.jpg"`,
			`AT+QFDEL="RAM:sd_outgoing.txt"`,
		}
		for _, expected := range expectedCommands {
			Eventually(commands).Should(Receive(Equal(expected)))
		}
		Eventually(uploads).Should(Receive(Equal(image)))
		Eventually(uploads).Should(Receive(Equal([]byte("hello"))))
	})

	It("does not accept a rejected or incomplete SMS submission", func() {
		for _, response := range []string{"+CMS ERROR: 500\r\n", "OK\r\n"} {
			session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
			go func(response string) {
				reader := bufio.NewReader(modemTransport)
				_, _ = reader.ReadString('\r')
				_, _ = modemTransport.Write([]byte("\r\n> "))
				_, _ = reader.ReadBytes(0x1a)
				_, _ = modemTransport.Write([]byte(response))
			}(response)

			err := session.SendSMS(context.Background(), "13500000000", "hello", 1)

			Expect(err).To(HaveOccurred())
			_ = client.Close()
			_ = modemTransport.Close()
		}
	})

	It("logs modem rejection details without recipient or message content", func() {
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		var logs bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logs, nil))
		session, err := NewSession(
			client,
			sms.NewInbox(time.Hour),
			MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }),
			logger,
			100*time.Millisecond,
		)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("\r\n> "))
			_, _ = reader.ReadBytes(0x1a)
			_, _ = modemTransport.Write([]byte("\r\n+CMS ERROR: 500\r\n"))
		}()

		err = session.SendSMS(context.Background(), "13500000000", "private body", 1)

		Expect(err).To(MatchError(ContainSubstring("+CMS ERROR: 500")))
		Expect(logs.String()).To(ContainSubstring("SMS submission failed"))
		Expect(logs.String()).To(ContainSubstring("terminal=\"+CMS ERROR: 500\""))
		Expect(logs.String()).NotTo(ContainSubstring("13500000000"))
		Expect(logs.String()).NotTo(ContainSubstring("private body"))
	})

	It("rejects an invalid outbound SMS before writing to the modem", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		Expect(session.SendSMS(context.Background(), "invalid", "hello", 1)).To(MatchError(ContainSubstring("encode outbound SMS")))
	})

	DescribeTable("parses asynchronous MMS submission results",
		func(urc string, succeeds bool) {
			err := parseMMSResult(urc)
			if succeeds {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("success", "+QMMSEND: 0,200", true),
		Entry("modem rejection", "+QMMSEND: 1,500", false),
		Entry("malformed", "+QMMSEND: invalid", false),
	)

	It("retries a deferred durable SMS without restarting the modem", func() {
		attempts := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			attempts <- message
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		session.messageRetryInterval = 5 * time.Millisecond
		_, created, err := session.inbox.AddSegment(sms.Message{
			Sender: "13500000000", Timestamp: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), Text: "retry me",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		Eventually(attempts).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("retry me"))))
		Eventually(session.inbox.PendingMessages).Should(BeEmpty())
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("polls combined storage so a missed SMS notification is recovered", func() {
		received := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			received <- message
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		session.messagePollInterval = 5 * time.Millisecond
		commands := make(chan string, 8)
		go func() {
			reader := bufio.NewReader(modemTransport)
			first := true
			for {
				command, err := reader.ReadString('\r')
				if err != nil {
					return
				}
				select {
				case commands <- strings.TrimSuffix(command, "\r"):
				default:
				}
				if first {
					first = false
					_, _ = modemTransport.Write([]byte("+CMGL: 4,0,,23\r\n" + testDeliverPDU + "\r\nOK\r\n"))
				} else {
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		Eventually(commands).Should(Receive(Equal("AT+CMGL=4")))
		Eventually(received, 2*time.Second).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("你好"))))
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("surfaces a terminal pending SMS delivery failure", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return errors.New("encrypted outbox failed")
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		_, created, err := session.inbox.AddSegment(sms.Message{
			Sender: "13500000000", Timestamp: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), Text: "keep me",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		Expect(session.RetryPendingSMS(context.Background())).To(MatchError("deliver imported SMS: encrypted outbox failed"))
		Expect(session.inbox.PendingMessages()).To(HaveLen(1))
	})

	It("stops when periodic pending SMS delivery fails terminally", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return errors.New("encrypted outbox failed")
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		session.messageRetryInterval = 5 * time.Millisecond
		_, created, err := session.inbox.AddSegment(sms.Message{
			Sender: "13500000000", Timestamp: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC), Text: "retry fails",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(created).To(BeTrue())

		Expect(session.Run(context.Background())).To(MatchError("deliver imported SMS: encrypted outbox failed"))
	})

	It("fails initialization when the modem does not answer before the timeout", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
		}()

		err := session.Initialize(context.Background())

		Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	})

	DescribeTable("rejects a modem without a ready SIM",
		func(response string, expected string) {
			session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
			DeferCleanup(func() {
				_ = client.Close()
				_ = modemTransport.Close()
			})
			go func() {
				reader := bufio.NewReader(modemTransport)
				for attempt := 0; attempt < simReadinessAttempts; attempt++ {
					if _, err := reader.ReadString('\r'); err != nil {
						return
					}
					_, _ = modemTransport.Write([]byte(response))
				}
			}()

			Expect(session.Initialize(context.Background())).To(MatchError(expected))
		},
		Entry("when the SIM requires a PIN", "+CPIN: SIM PIN\r\nOK\r\n", "modem SIM is not ready: SIM PIN"),
		Entry("when the modem omits CPIN status", "OK\r\n", "modem SIM readiness response is missing +CPIN"),
		Entry("when the modem rejects the query", "+CME ERROR: SIM not inserted\r\n", "modem rejected SIM readiness query: +CME ERROR: SIM not inserted"),
	)

	It("retries a successful SIM query when a stale response omits CPIN", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			if command == simReadinessCommand+"\r" {
				_, _ = modemTransport.Write([]byte("OK\r\n"))
			}
			command, _ = reader.ReadString('\r')
			if command == simReadinessCommand+"\r" {
				_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
			}
			for index := 0; index < len(initializationCommands)+2; index++ {
				_, _ = reader.ReadString('\r')
				_, _ = modemTransport.Write([]byte("OK\r\n"))
			}
		}()

		Expect(session.Initialize(context.Background())).NotTo(HaveOccurred())
	})

	It("fails initialization when the history command is rejected", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+3; index++ {
				command, _ := reader.ReadString('\r')
				switch command {
				case simReadinessCommand + "\r":
					_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
				case "AT+CMGL=4\r":
					_, _ = modemTransport.Write([]byte("ERROR\r\n"))
				default:
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		err := session.Initialize(context.Background())

		Expect(err).To(MatchError("import SMS history: modem rejected stored SMS scan: ERROR"))
	})

	It("returns a history sink failure instead of silently losing the message", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return errors.New("outbox unavailable")
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+3; index++ {
				command, _ := reader.ReadString('\r')
				switch command {
				case simReadinessCommand + "\r":
					_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
				case "AT+CMGL=4\r":
					_, _ = modemTransport.Write([]byte("+CMGL: 1,0,,23\r\n" + testDeliverPDU + "\r\nOK\r\n"))
				default:
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		err := session.Initialize(context.Background())

		Expect(err).To(MatchError("deliver imported SMS: outbox unavailable"))
	})

	It("keeps imported SMS pending when encrypted delivery is deferred", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return ErrMessageDeliveryDeferred
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			for index := 0; index < len(initializationCommands)+3; index++ {
				command, _ := reader.ReadString('\r')
				switch command {
				case simReadinessCommand + "\r":
					_, _ = modemTransport.Write([]byte("+CPIN: READY\r\nOK\r\n"))
				case "AT+CMGL=4\r":
					_, _ = modemTransport.Write([]byte("+CMGL: 1,0,,23\r\n" + testDeliverPDU + "\r\nOK\r\n"))
				default:
					_, _ = modemTransport.Write([]byte("OK\r\n"))
				}
			}
		}()

		Expect(session.Initialize(context.Background())).To(Succeed())

		pending := session.inbox.PendingMessages()
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].Text).To(Equal("你好"))
	})

	It("routes direct SMS and concurrent call notifications", func() {
		received := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			received <- message
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n" + testDeliverPDU + "\r\nRING\r\n+CLIP: \"13500000000\",129,\"\",0,\"\",0\r\n+CCWA: \"10086\",129,1\r\n"))

		Eventually(received).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("你好"))))
		Eventually(func() call.Snapshot { return session.Calls().Snapshot() }).Should(Equal(call.Snapshot{State: call.StateWaiting, Waiting: true, IncomingNumber: "10086"}))
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("reads stored SMS notifications and deletes them after encrypted delivery", func() {
		received := make(chan sms.ReceivedMessage, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(_ context.Context, message sms.ReceivedMessage) error {
			received <- message
			return nil
		}))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CMGR=7"))
			_, _ = modemTransport.Write([]byte("+CMGR: 1,,23\r\n" + testDeliverPDU + "\r\nOK\r\n"))
			command, _ = reader.ReadString('\r')
			Expect(strings.TrimSuffix(command, "\r")).To(Equal("AT+CMGD=7"))
			_, _ = modemTransport.Write([]byte("OK\r\n"))
		}()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMTI: \"ME\",7\r\n"))
		Eventually(received).Should(Receive(WithTransform(func(message sms.ReceivedMessage) string { return message.Text }, Equal("你好"))))
		Eventually(session.inbox.PendingMessages).Should(HaveLen(0))
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("retains stored SMS when encrypted delivery is deferred", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return ErrMessageDeliveryDeferred
		}))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		commands := make(chan string, 2)
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			commands <- strings.TrimSuffix(command, "\r")
			_, _ = modemTransport.Write([]byte("+CMGR: 1,,23\r\n" + testDeliverPDU + "\r\nOK\r\n"))
			command, _ = reader.ReadString('\r')
			commands <- strings.TrimSuffix(command, "\r")
			_, _ = modemTransport.Write([]byte("OK\r\n"))
		}()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMTI: \"ME\",8\r\n"))
		Eventually(commands).Should(Receive(Equal("AT+CMGR=8")))
		Eventually(session.inbox.PendingMessages).Should(HaveLen(1))
		Eventually(commands).Should(Receive(Equal("AT+CMGD=8")))
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("continues after malformed and informational notifications", func() {
		sinkCalls := make(chan struct{}, 1)
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			sinkCalls <- struct{}{}
			return nil
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,1\r\ninvalid\r\n+CDS: 1\r\n00\r\n+CMTI: malformed\r\n"))
		Consistently(sinkCalls, 50*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("returns a direct-message sink failure", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return errors.New("outbox unavailable")
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		result := make(chan error, 1)
		go func() { result <- session.Run(context.Background()) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n" + testDeliverPDU + "\r\n"))

		Eventually(result).Should(Receive(MatchError("deliver direct SMS: outbox unavailable")))
	})

	It("keeps running when direct SMS delivery is deferred", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error {
			return ErrMessageDeliveryDeferred
		}))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- session.Run(ctx) }()

		_, _ = modemTransport.Write([]byte("+CMT: ,23\r\n" + testDeliverPDU + "\r\n"))

		Eventually(session.inbox.PendingMessages).Should(HaveLen(1))
		Consistently(result, 50*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(result).Should(Receive(MatchError(context.Canceled)))
	})

	It("returns the terminal serial error", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close() })
		result := make(chan error, 1)
		go func() { result <- session.Run(context.Background()) }()

		_ = modemTransport.Close()

		Eventually(result).Should(Receive(MatchError(io.EOF)))
	})

	It("polls CLCC for an authoritative connected state", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		session.Calls().ApplyURC("RING")
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(command).To(Equal("AT+CLCC\r"))
			_, _ = modemTransport.Write([]byte("+CLCC: 1,1,0,0,0,\"13500000000\",129\r\nOK\r\n"))
		}()

		Expect(session.refreshCallState(context.Background())).To(Succeed())

		Expect(session.Calls().Snapshot()).To(Equal(call.Snapshot{State: call.StateActive, Active: true}))
	})

	It("leaves an idle modem untouched during startup call reconciliation", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(command).To(Equal("AT+CLCC\r"))
			_, _ = modemTransport.Write([]byte("OK\r\n"))
		}()

		Expect(session.ClearOrphanedCalls(context.Background())).To(Succeed())
		Expect(session.Calls().State()).To(Equal(call.StateIdle))
	})

	DescribeTable("ends carrier calls that survived the previous agent process",
		func(clcc string) {
			session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
			DeferCleanup(func() {
				_ = client.Close()
				_ = modemTransport.Close()
			})
			go func() {
				reader := bufio.NewReader(modemTransport)
				command, _ := reader.ReadString('\r')
				Expect(command).To(Equal("AT+CLCC\r"))
				_, _ = modemTransport.Write([]byte(clcc + "\r\nOK\r\n"))
				command, _ = reader.ReadString('\r')
				Expect(command).To(Equal("ATH\r"))
				_, _ = modemTransport.Write([]byte("OK\r\n"))
			}()

			Expect(session.ClearOrphanedCalls(context.Background())).To(Succeed())
			Expect(session.Calls().State()).To(Equal(call.StateEnded))
		},
		Entry("active", "+CLCC: 1,1,0,0,0"),
		Entry("held", "+CLCC: 1,1,1,0,0"),
		Entry("dialing", "+CLCC: 1,0,2,0,0"),
		Entry("ringing", "+CLCC: 1,1,4,0,0"),
		Entry("waiting", "+CLCC: 1,1,5,0,0"),
	)

	It("fails startup call reconciliation when CLCC is rejected", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			_, _ = bufio.NewReader(modemTransport).ReadString('\r')
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
		}()

		Expect(session.ClearOrphanedCalls(context.Background())).To(MatchError("modem rejected orphaned call query: ERROR"))
	})

	It("reports malformed orphaned call state during startup", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			_, _ = bufio.NewReader(modemTransport).ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CLCC: malformed\r\nOK\r\n"))
		}()

		Expect(session.ClearOrphanedCalls(context.Background())).To(MatchError(ContainSubstring("parse orphaned modem calls")))
	})

	It("reports a failed orphaned call hangup", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CLCC: 1,1,0,0,0\r\nOK\r\n"))
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
		}()

		Expect(session.ClearOrphanedCalls(context.Background())).To(MatchError("hang up orphaned modem call: modem rejected ATH: ERROR"))
	})

	It("polls idle CLCC so an incoming call is detected when the modem omits URCs", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			command, _ := reader.ReadString('\r')
			Expect(command).To(Equal("AT+CLCC\r"))
			_, _ = modemTransport.Write([]byte("+CLCC: 1,1,4,0,0,\"13500000000\",129\r\nOK\r\n"))
		}()

		Expect(session.refreshCallState(context.Background())).To(Succeed())
		Expect(session.Calls().Snapshot()).To(Equal(call.Snapshot{
			State: call.StateRinging, IncomingNumber: "13500000000",
		}))
	})

	It("maps CLCC query failures", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("ERROR\r\n"))
		}()
		Expect(session.refreshCallState(context.Background())).To(MatchError("modem rejected call state query: ERROR"))
	})

	It("rejects malformed CLCC responses", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})
		session.Calls().ApplyURC("RING")
		go func() {
			reader := bufio.NewReader(modemTransport)
			_, _ = reader.ReadString('\r')
			_, _ = modemTransport.Write([]byte("+CLCC: malformed\r\nOK\r\n"))
		}()
		Expect(session.refreshCallState(context.Background())).To(MatchError(ContainSubstring("parse modem call state")))
	})

	It("extracts complete history entries and reports missing payloads", func() {
		pdus, err := historyPDUs([]string{"echo", "+CMGL: 1", "AABB", "+CMGL: 2", "+CMGL: 3", "CCDD"})

		Expect(pdus).To(Equal([]string{"AABB", "CCDD"}))
		Expect(err).To(MatchError(ContainSubstring("response line 3 has no PDU")))
	})

	It("tracks SMS delivery report metadata and parses modem helper values", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		reportSink := func(context.Context, sms.DeliveryReport) error { return nil }
		session.SetDeliveryReportSink(reportSink)
		session.PrepareMessageStatus("message-1")
		Expect(session.deliveryReportSink).NotTo(BeNil())
		Expect(session.pendingMessageID).To(Equal("message-1"))

		reference, ok := parseMessageReference([]string{"+CMGS: 42"})
		Expect(reference).To(Equal(byte(42)))
		Expect(ok).To(BeTrue())
		_, ok = parseMessageReference([]string{"+CMGS: invalid"})
		Expect(ok).To(BeFalse())
		Expect(hasMessageReference([]string{"+CMGS: 42"})).To(BeTrue())
		Expect(hasMessageReference([]string{"+CMGS: "})).To(BeFalse())
		index, ok := storedMessageIndex("+CMTI: \"ME\", 7")
		Expect(index).To(Equal(7))
		Expect(ok).To(BeTrue())
		_, ok = storedMessageIndex("+CMTI: malformed")
		Expect(ok).To(BeFalse())
		pdu, ok := storedMessagePDU([]string{"+CMGR: 1,,23", testDeliverPDU})
		Expect(pdu).To(Equal(testDeliverPDU))
		Expect(ok).To(BeTrue())
		_, ok = storedMessagePDU([]string{"+CMGR: 1,,23", "+ERROR"})
		Expect(ok).To(BeFalse())
	})

	It("publishes a correlated SMS delivery report", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		reports := make(chan sms.DeliveryReport, 1)
		session.references[7] = "message-7"
		session.SetDeliveryReportSink(func(_ context.Context, report sms.DeliveryReport) error {
			reports <- report
			return nil
		})

		Expect(session.handleURC(context.Background(), "+CDS: 18\n0002070B913105000000F0620151214365006201512143650000")).To(Succeed())
		report := <-reports
		Expect(report.MessageID).To(Equal("message-7"))
		Expect(report.Status).To(Equal(sms.DeliveryDelivered))
		_, exists := session.references[7]
		Expect(exists).To(BeFalse())
	})

	It("synchronizes delivery report correlation across command and URC workers", func() {
		session, modemTransport, client := newTestSession(MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil }))
		DeferCleanup(func() { _ = client.Close(); _ = modemTransport.Close() })
		reports := make(chan sms.DeliveryReport, 256)
		session.SetDeliveryReportSink(func(_ context.Context, report sms.DeliveryReport) error {
			reports <- report
			return nil
		})

		completed := make(chan struct{})
		mismatches := make(chan string, 1)
		go func() {
			defer close(completed)
			for index := 0; index < 256; index++ {
				messageID := fmt.Sprintf("message-%d", index)
				session.PrepareMessageStatus(messageID)
				if actual := session.takePendingMessageID(); actual != messageID {
					mismatches <- fmt.Sprintf("pending message ID = %q, want %q", actual, messageID)
					return
				}
				session.trackMessageReference(byte(index), messageID)
			}
		}()
		for index := 0; index < 256; index++ {
			_ = session.handleURC(context.Background(), "+CDS: 18\n0002070B913105000000F0620151214365006201512143650000")
		}
		<-completed
		Consistently(mismatches).ShouldNot(Receive())

		session.trackMessageReference(7, "message-final")
		Expect(session.handleURC(context.Background(), "+CDS: 18\n0002070B913105000000F0620151214365006201512143650000")).To(Succeed())
		Eventually(reports).Should(Receive(WithTransform(func(report sms.DeliveryReport) string {
			return report.MessageID
		}, Equal("message-final"))))
	})

	DescribeTable("classifies call notifications", func(notification string, expected bool) {
		Expect(isCallURC(notification)).To(Equal(expected))
	},
		Entry("ring", "RING", true),
		Entry("caller ID", "+CLIP: 1", true),
		Entry("call waiting", "+CCWA: 1", true),
		Entry("no carrier", "NO CARRIER", true),
		Entry("no answer", "NO ANSWER", true),
		Entry("busy", "BUSY", true),
		Entry("unrelated", "READY", false),
	)

	DescribeTable("classifies notification metadata without exposing payloads",
		func(notification string, expected string) {
			Expect(notificationType(notification)).To(Equal(expected))
		},
		Entry("prefixed", "+CMTI: \"ME\",1", "+CMTI"),
		Entry("multiline", "HEADER\npayload", "multiline"),
		Entry("plain", "READY", "READY"),
	)

	It("validates constructor dependencies", func() {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		sink := MessageSinkFunc(func(context.Context, sms.ReceivedMessage) error { return nil })
		clientTransport, modemTransport := net.Pipe()
		client := at.NewClient(clientTransport)
		DeferCleanup(func() {
			_ = client.Close()
			_ = modemTransport.Close()
		})

		for _, build := range []func() (*Session, error){
			func() (*Session, error) { return NewSession(nil, sms.NewInbox(time.Hour), sink, logger, time.Second) },
			func() (*Session, error) { return NewSession(client, nil, sink, logger, time.Second) },
			func() (*Session, error) { return NewSession(client, sms.NewInbox(time.Hour), nil, logger, time.Second) },
			func() (*Session, error) { return NewSession(client, sms.NewInbox(time.Hour), sink, nil, time.Second) },
			func() (*Session, error) { return NewSession(client, sms.NewInbox(time.Hour), sink, logger, 0) },
		} {
			_, err := build()
			Expect(err).To(MatchError("modem session dependencies and positive timeout are required"))
		}
	})
})
