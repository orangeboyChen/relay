package apns

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAPNs(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "APNs Suite")
}
