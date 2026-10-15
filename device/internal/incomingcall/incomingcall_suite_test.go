package incomingcall

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestIncomingCall(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Incoming Call Suite")
}
