package tests

import (
	"testing"

	"github.com/gruntwork-io/terratest/modules/helm"
	"github.com/gruntwork-io/terratest/modules/logger"
)

func TestAutoscalingRunnerSetLabels(t *testing.T) {
	t.Parallel()

	t.Run("should set default labels", func(t *testing.T) {
		t.Parallel()

		options := &helm.Options{
			Logger: logger.Discard,
		}
	})
}
