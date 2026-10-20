package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ALSA modem identity validation", func() {
	DescribeTable("derives a control device from a PCM name",
		func(pcm string, expected string) {
			control, err := alsaControlDevice(pcm)
			Expect(err).NotTo(HaveOccurred())
			Expect(control).To(Equal(expected))
		},
		Entry("numeric hardware PCM", "hw:0,0", "hw:0"),
		Entry("numeric plug PCM", "plughw:2,1", "hw:2"),
		Entry("named hardware PCM", "hw:CARD=Android,DEV=0", "hw:CARD=Android"),
		Entry("named plug PCM", "plughw:CARD=Android,DEV=0", "hw:CARD=Android"),
	)

	It("rejects an empty PCM name", func() {
		_, err := alsaControlDevice(" ")

		Expect(err).To(MatchError("ALSA capture and playback devices are required"))
	})

	It("runs the production amixer inspection command", func() {
		binDirectory := GinkgoT().TempDir()
		amixer := filepath.Join(binDirectory, "amixer")
		Expect(os.WriteFile(
			amixer,
			[]byte("#!/bin/sh\nprintf '%s\\n' \"Card hw:0 'Android'/'Android Android'\" \"  Components: 'USB2c7c:0125'\"\n"),
			0o700,
		)).To(Succeed())
		GinkgoT().Setenv("PATH", binDirectory)

		Expect(ValidateALSADevices(context.Background(), "hw:0,0", "hw:0,0")).To(Succeed())
	})

	It("accepts the same Quectel UAC card for capture and playback", func() {
		var calls []string
		runner := func(_ context.Context, executable string, arguments ...string) ([]byte, error) {
			calls = append(calls, executable+" "+arguments[1])
			return []byte("Card hw:0 'Android'/'Android Android'\n  Components: 'USB2c7c:0125'\n"), nil
		}

		err := validateALSADevices(context.Background(), "hw:0,0", "hw:0,0", runner)

		Expect(err).NotTo(HaveOccurred())
		Expect(calls).To(Equal([]string{"amixer hw:0"}))
	})

	It("rejects a stale mapping to an unrelated USB audio card", func() {
		runner := func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Card hw:0 'HID'/'CSCTEK USB Audio and HID'\n  Components: 'USB0573:1573'\n"), nil
		}

		err := validateALSADevices(context.Background(), "hw:0,0", "hw:0,0", runner)

		Expect(err).To(MatchError(And(
			ContainSubstring("not the Quectel modem USB audio interface"),
			ContainSubstring("CSCTEK USB Audio and HID"),
		)))
	})

	It("reports inspection failures before opening audio", func() {
		runner := func(context.Context, string, ...string) ([]byte, error) {
			return []byte("cannot open mixer"), errors.New("exit status 1")
		}

		err := validateALSADevices(context.Background(), "hw:0,0", "hw:0,0", runner)

		Expect(err).To(MatchError(`inspect ALSA device "hw:0": cannot open mixer`))
	})

	It("uses the process error when inspection has no output", func() {
		runner := func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("amixer unavailable")
		}

		err := validateALSADevices(context.Background(), "hw:0,0", "hw:0,0", runner)

		Expect(err).To(MatchError(`inspect ALSA device "hw:0": amixer unavailable`))
	})

	It("requires an inspector", func() {
		err := validateALSADevices(context.Background(), "hw:0,0", "hw:0,0", nil)

		Expect(err).To(MatchError("ALSA device inspector is required"))
	})

	It("reports an empty identity response", func() {
		runner := func(context.Context, string, ...string) ([]byte, error) {
			return []byte("\n"), nil
		}

		err := validateALSADevices(context.Background(), "hw:0,0", "hw:0,0", runner)

		Expect(err).To(MatchError(ContainSubstring("no ALSA card identity returned")))
	})

	It("inspects distinct capture and playback cards", func() {
		var devices []string
		runner := func(_ context.Context, _ string, arguments ...string) ([]byte, error) {
			devices = append(devices, arguments[1])
			return []byte("Components: 'USB2c7c:0125'"), nil
		}

		Expect(validateALSADevices(
			context.Background(),
			"hw:0,0",
			"hw:1,0",
			runner,
		)).To(Succeed())
		Expect(devices).To(Equal([]string{"hw:0", "hw:1"}))
	})
})
