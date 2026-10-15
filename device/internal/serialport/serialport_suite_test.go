package serialport

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSerialPort(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Serial Port Suite")
}
