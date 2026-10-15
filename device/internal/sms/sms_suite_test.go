package sms

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSMS(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SMS Suite")
}
