package capacity

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func setEnvs(t *testing.T, envs map[string]string) {
	t.Helper()
	for k, v := range envs {
		prev, existed := os.LookupEnv(k)
		os.Setenv(k, v)
		if existed {
			t.Cleanup(func() { os.Setenv(k, prev) })
		} else {
			t.Cleanup(func() { os.Unsetenv(k) })
		}
	}
}

func unsetEnvs(t *testing.T, keys []string) {
	t.Helper()
	for _, k := range keys {
		prev, existed := os.LookupEnv(k)
		os.Unsetenv(k)
		if existed {
			t.Cleanup(func() { os.Setenv(k, prev) })
		}
	}
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	keys := []string{
		"CAPACITY_AWARE_ENABLED",
		"CAPACITY_AWARE_PROACTIVE_CAPACITY",
		"CAPACITY_AWARE_RECALCULATE_INTERVAL",
		"CAPACITY_AWARE_PLACEHOLDER_TIMEOUT",
		"CAPACITY_AWARE_WORKFLOW_CPU",
		"CAPACITY_AWARE_WORKFLOW_MEMORY",
		"CAPACITY_AWARE_WORKFLOW_GPU",
		"CAPACITY_AWARE_WORKFLOW_DISK",
		"CAPACITY_AWARE_RUNNER_CPU",
		"CAPACITY_AWARE_RUNNER_MEMORY",
		"CAPACITY_AWARE_NODE_FLEET",
		"CAPACITY_AWARE_RUNNER_CLASS",
		"CAPACITY_AWARE_HUD_API_TOKEN",
	}
	unsetEnvs(t, keys)

	cfg := ConfigFromEnv()

	assert.False(t, cfg.Enabled, "Enabled default")
	assert.Equal(t, 0, cfg.ProactiveCapacity, "ProactiveCapacity default")
	assert.Equal(t, 30*time.Second, cfg.RecalculateInterval, "RecalculateInterval default")
	assert.Equal(t, 5*time.Minute, cfg.PlaceholderTimeout, "PlaceholderTimeout default")
	assert.Equal(t, "", cfg.WorkflowCPU, "WorkflowCPU default")
	assert.Equal(t, "", cfg.WorkflowMemory, "WorkflowMemory default")
	assert.Equal(t, 0, cfg.WorkflowGPU, "WorkflowGPU default")
	assert.Equal(t, "", cfg.WorkflowDisk, "WorkflowDisk default")
	assert.Equal(t, "750m", cfg.RunnerCPU, "RunnerCPU default")
	assert.Equal(t, "512Mi", cfg.RunnerMemory, "RunnerMemory default")
	assert.Equal(t, "", cfg.NodeFleet, "NodeFleet default")
	assert.Equal(t, "", cfg.RunnerClass, "RunnerClass default")
	assert.Equal(t, "", cfg.HUDAPIToken, "HUDAPIToken default")
	// Fields set by main.go should be zero values.
	assert.Equal(t, 0, cfg.MaxRunners, "MaxRunners zero")
	assert.Equal(t, 0, cfg.ScaleSetID, "ScaleSetID zero")
	assert.Nil(t, cfg.ScaleSetLabels, "ScaleSetLabels nil")
	assert.Equal(t, "", cfg.Namespace, "Namespace zero")
	assert.Equal(t, "", cfg.ScaleSetName, "ScaleSetName zero")
}

func TestConfigFromEnv_AllSet(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_ENABLED":              "true",
		"CAPACITY_AWARE_PROACTIVE_CAPACITY":   "5",
		"CAPACITY_AWARE_RECALCULATE_INTERVAL": "10s",
		"CAPACITY_AWARE_PLACEHOLDER_TIMEOUT":  "2m",
		"CAPACITY_AWARE_WORKFLOW_CPU":         "4",
		"CAPACITY_AWARE_WORKFLOW_MEMORY":      "8Gi",
		"CAPACITY_AWARE_WORKFLOW_GPU":         "2",
		"CAPACITY_AWARE_WORKFLOW_DISK":        "100Gi",
		"CAPACITY_AWARE_RUNNER_CPU":           "1",
		"CAPACITY_AWARE_RUNNER_MEMORY":        "1Gi",
		"CAPACITY_AWARE_NODE_FLEET":           "gpu-fleet",
		"CAPACITY_AWARE_RUNNER_CLASS":         "gpu-large",
		"CAPACITY_AWARE_HUD_API_TOKEN":        "secret-token",
	})

	cfg := ConfigFromEnv()

	assert.True(t, cfg.Enabled)
	assert.Equal(t, 5, cfg.ProactiveCapacity)
	assert.Equal(t, 10*time.Second, cfg.RecalculateInterval)
	assert.Equal(t, 2*time.Minute, cfg.PlaceholderTimeout)
	assert.Equal(t, "4", cfg.WorkflowCPU)
	assert.Equal(t, "8Gi", cfg.WorkflowMemory)
	assert.Equal(t, 2, cfg.WorkflowGPU)
	assert.Equal(t, "100Gi", cfg.WorkflowDisk)
	assert.Equal(t, "1", cfg.RunnerCPU)
	assert.Equal(t, "1Gi", cfg.RunnerMemory)
	assert.Equal(t, "gpu-fleet", cfg.NodeFleet)
	assert.Equal(t, "gpu-large", cfg.RunnerClass)
	assert.Equal(t, "secret-token", cfg.HUDAPIToken)
}

func TestConfigFromEnv_InvalidValues_FallbackToDefaults(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_ENABLED":              "not-a-bool",
		"CAPACITY_AWARE_PROACTIVE_CAPACITY":   "not-an-int",
		"CAPACITY_AWARE_RECALCULATE_INTERVAL": "not-a-duration",
		"CAPACITY_AWARE_PLACEHOLDER_TIMEOUT":  "999",
		"CAPACITY_AWARE_WORKFLOW_GPU":         "abc",
	})

	cfg := ConfigFromEnv()

	assert.False(t, cfg.Enabled, "invalid bool falls back to false")
	assert.Equal(t, 0, cfg.ProactiveCapacity, "invalid int falls back to 0")
	assert.Equal(t, 30*time.Second, cfg.RecalculateInterval, "invalid duration falls back to 30s")
	assert.Equal(t, 5*time.Minute, cfg.PlaceholderTimeout, "invalid duration falls back to 5m")
	assert.Equal(t, 0, cfg.WorkflowGPU, "invalid int falls back to 0")
}

func TestConfigFromEnv_WhitespaceTrimmmed(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_ENABLED":            "  true  ",
		"CAPACITY_AWARE_PROACTIVE_CAPACITY": "  3  ",
		"CAPACITY_AWARE_NODE_FLEET":         "  my-fleet  ",
	})

	cfg := ConfigFromEnv()

	assert.True(t, cfg.Enabled)
	assert.Equal(t, 3, cfg.ProactiveCapacity)
	assert.Equal(t, "my-fleet", cfg.NodeFleet)
}

// Negative ProactiveCapacity must be clamped to 0 — never used as a
// negative (which would underflow downstream arithmetic).
func TestConfigFromEnv_ProactiveCapacity_NegativeClampedToZero(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_PROACTIVE_CAPACITY": "-5",
	})

	cfg := ConfigFromEnv()

	assert.Equal(t, 0, cfg.ProactiveCapacity,
		"negative ProactiveCapacity must clamp to 0")
}

// Values above the hard cap (1000) must be clamped — protects against
// runaway placeholder creation from a misconfiguration.
func TestConfigFromEnv_ProactiveCapacity_AboveHardCapClamped(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_PROACTIVE_CAPACITY": "5000",
	})

	cfg := ConfigFromEnv()

	assert.Equal(t, proactiveCapacityHardCap, cfg.ProactiveCapacity,
		"value above hard cap must clamp to %d", proactiveCapacityHardCap)
}

// Values exactly at the hard cap are allowed (boundary).
func TestConfigFromEnv_ProactiveCapacity_AtHardCapAllowed(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_PROACTIVE_CAPACITY": "1000",
	})

	cfg := ConfigFromEnv()

	assert.Equal(t, proactiveCapacityHardCap, cfg.ProactiveCapacity,
		"value exactly at hard cap must be preserved")
}

// Values above the warn threshold (100) but below the hard cap (1000)
// are allowed unchanged — operators may legitimately need this in surge.
func TestConfigFromEnv_ProactiveCapacity_AboveWarnAllowed(t *testing.T) {
	setEnvs(t, map[string]string{
		"CAPACITY_AWARE_PROACTIVE_CAPACITY": "250",
	})

	cfg := ConfigFromEnv()

	assert.Equal(t, 250, cfg.ProactiveCapacity,
		"values between warn threshold and hard cap are allowed")
}

// Validate() clamps negative MaxRunners (set by main.go after env parse).
func TestConfig_Validate_MaxRunnersNegativeClamped(t *testing.T) {
	cfg := Config{MaxRunners: -3}
	cfg.Validate()
	assert.Equal(t, 0, cfg.MaxRunners,
		"Validate must clamp negative MaxRunners to 0")
}

func TestConfig_Validate_MaxRunnersZeroPreserved(t *testing.T) {
	cfg := Config{MaxRunners: 0}
	cfg.Validate()
	assert.Equal(t, 0, cfg.MaxRunners,
		"Validate preserves MaxRunners=0 (means unlimited downstream)")
}

func TestConfig_Validate_MaxRunnersPositivePreserved(t *testing.T) {
	cfg := Config{MaxRunners: 42}
	cfg.Validate()
	assert.Equal(t, 42, cfg.MaxRunners,
		"Validate preserves positive MaxRunners")
}
