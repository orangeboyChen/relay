package serverapi

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestServerAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Server API Suite")
}
