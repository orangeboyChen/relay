package modem

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestModem(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Modem Suite")
}
