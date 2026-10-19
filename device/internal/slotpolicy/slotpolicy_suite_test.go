package slotpolicy

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSlotPolicy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Slot Policy Suite")
}
