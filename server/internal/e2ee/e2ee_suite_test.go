package e2ee_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestE2EE(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "E2EE Suite")
}
