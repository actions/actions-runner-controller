package actions_test

import (
	"testing"

	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/stretchr/testify/assert"
)

func TestUserAgentInfoString(t *testing.T) {
	userAgentInfo := actions.UserAgentInfo{
		Version:    "0.1.0",
		CommitSHA:  "1234567890abcdef",
		ScaleSetID: 10,
		HasProxy:   true,
		Subsystem:  "test",
	}

	userAgent := userAgentInfo.String()
	expectedProduct := "actions-runner-controller/0.1.0 (1234567890abcdef; test)"
	assert.Contains(t, userAgent, expectedProduct)
	expectedScaleSet := "ScaleSetID/10 (Proxy/enabled)"
	assert.Contains(t, userAgent, expectedScaleSet)
}
