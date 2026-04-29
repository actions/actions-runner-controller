package capacity

import (
	"errors"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// proactiveCapacityHardCap is the absolute maximum allowed value;
	// anything larger is clamped to prevent runaway placeholder creation.
	proactiveCapacityHardCap = 1000
	// proactiveCapacityWarnThreshold triggers a warning log but does
	// not clamp — operators may legitimately need >100 in surge cases.
	proactiveCapacityWarnThreshold = 100
)

// Config holds all configuration for the capacity monitor.
// Fields marked "set by main.go" are populated after ConfigFromEnv returns.
type Config struct {
	Enabled             bool
	ProactiveCapacity   int
	RecalculateInterval time.Duration
	ReportInterval      time.Duration
	PlaceholderTimeout  time.Duration
	MaxRunners          int

	// Workflow pod resources (for placeholder-workflow sizing)
	WorkflowCPU    string
	WorkflowMemory string
	WorkflowGPU    int
	WorkflowDisk   string

	// Runner pod resources (for placeholder-runner sizing)
	RunnerCPU    string
	RunnerMemory string

	// Node placement.
	// NodeFleet is the workflow-pool fleet, used for placeholder-workflow pods.
	// Per-scale-set value (e.g. g4dn, c7a, m8g).
	// RunnerNodeFleet is the runner-pool fleet, used for placeholder-runner pods.
	// Cluster-wide value (currently c7i-runner) — same across all scale sets.
	NodeFleet       string
	RunnerNodeFleet string
	RunnerClass     string

	// Scale set info (set by main.go, not env vars)
	ScaleSetID     int
	ScaleSetLabels []string
	Namespace      string // runner namespace (EphemeralRunnerSetNamespace)

	// Scale set name for label selectors (set by main.go)
	ScaleSetName string

	// HUD API
	HUDAPIURL   string
	HUDAPIToken string
}

// ConfigFromEnv reads capacity monitor configuration from environment
// variables. Fields that come from the listener config (MaxRunners,
// ScaleSetID, ScaleSetLabels, Namespace, ScaleSetName) are left at
// zero values and must be set by the caller.
func ConfigFromEnv() Config {
	c := Config{
		Enabled:             envBool("CAPACITY_AWARE_ENABLED", false),
		ProactiveCapacity:   envInt("CAPACITY_AWARE_PROACTIVE_CAPACITY", 0),
		RecalculateInterval: envDuration("CAPACITY_AWARE_RECALCULATE_INTERVAL", 30*time.Second),
		ReportInterval:      envDuration("CAPACITY_AWARE_REPORT_INTERVAL", 5*time.Second),
		PlaceholderTimeout:  envDuration("CAPACITY_AWARE_PLACEHOLDER_TIMEOUT", 5*time.Minute),
		WorkflowCPU:         envString("CAPACITY_AWARE_WORKFLOW_CPU", ""),
		WorkflowMemory:      envString("CAPACITY_AWARE_WORKFLOW_MEMORY", ""),
		WorkflowGPU:         envInt("CAPACITY_AWARE_WORKFLOW_GPU", 0),
		WorkflowDisk:        envString("CAPACITY_AWARE_WORKFLOW_DISK", ""),
		RunnerCPU:           envString("CAPACITY_AWARE_RUNNER_CPU", "750m"),
		RunnerMemory:        envString("CAPACITY_AWARE_RUNNER_MEMORY", "512Mi"),
		NodeFleet:           envString("CAPACITY_AWARE_NODE_FLEET", ""),
		RunnerNodeFleet:     envString("CAPACITY_AWARE_RUNNER_NODE_FLEET", ""),
		RunnerClass:         envString("CAPACITY_AWARE_RUNNER_CLASS", ""),
		HUDAPIURL:           envString("CAPACITY_AWARE_HUD_API_URL", defaultHUDAPIURL),
		HUDAPIToken:         envString("CAPACITY_AWARE_HUD_API_TOKEN", ""),
	}

	if c.ProactiveCapacity < 0 {
		slog.Warn("CAPACITY_AWARE_PROACTIVE_CAPACITY is negative, clamping to 0",
			"original", c.ProactiveCapacity)
		c.ProactiveCapacity = 0
	}
	if c.ProactiveCapacity > proactiveCapacityHardCap {
		slog.Warn("CAPACITY_AWARE_PROACTIVE_CAPACITY exceeds hard cap, clamping",
			"original", c.ProactiveCapacity, "cap", proactiveCapacityHardCap)
		c.ProactiveCapacity = proactiveCapacityHardCap
	} else if c.ProactiveCapacity > proactiveCapacityWarnThreshold {
		slog.Warn("CAPACITY_AWARE_PROACTIVE_CAPACITY is unusually high",
			"value", c.ProactiveCapacity, "warnThreshold", proactiveCapacityWarnThreshold)
	}

	return c
}

// Validate sanitizes fields populated by the caller (after ConfigFromEnv
// returns) and enforces required env vars when capacity-aware mode is
// enabled. Returns an error for any unrecoverable configuration problem.
//
// Side-effect: clamps negative MaxRunners to 0.
func (c *Config) Validate() error {
	if c.MaxRunners < 0 {
		slog.Warn("MaxRunners is negative, clamping to 0",
			"original", c.MaxRunners)
		c.MaxRunners = 0
	}

	if c.Enabled && c.RunnerNodeFleet == "" {
		// Hard requirement: the runner-pool fleet drives placeholder-runner
		// pod placement. Falling back to NodeFleet (the workflow-pool) would
		// silently land runner placeholders on the wrong pool — defeating the
		// topology separation that this config is here to provide.
		return errors.New(
			"CAPACITY_AWARE_RUNNER_NODE_FLEET is required when CAPACITY_AWARE_ENABLED=true",
		)
	}
	return nil
}

func envString(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return b
}

func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return i
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return d
}
