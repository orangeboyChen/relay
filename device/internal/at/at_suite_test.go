package at

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAT(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "AT Suite")
}
