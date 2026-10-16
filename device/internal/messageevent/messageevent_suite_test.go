package messageevent

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMessageEvent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Message Event Suite")
}
