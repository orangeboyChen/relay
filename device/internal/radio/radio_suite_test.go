package radio

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRadio(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Radio Suite")
}
