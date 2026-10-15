package callevent

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCallEvent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Call Event Suite")
}
