package call

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCall(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Call Suite")
}
