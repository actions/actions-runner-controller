package metrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A shared Server hands out one recorder per scale set. The recorders must share
// the same registered metrics (one HTTP endpoint) but stamp a distinct "name"
// label so a multiplexing listener does not collide variant series.
func TestServerRecorderPerScaleSet(t *testing.T) {
	srv := NewServer(ServerConfig{
		Enterprise:   "ent",
		Organization: "org",
		Repository:   "repo",
		ServerAddr:   ":0",
		Logger:       discardLogger,
	})

	a, ok := srv.RecorderFor("set-a", "ns").(*recorder)
	require.True(t, ok)
	b, ok := srv.RecorderFor("set-b", "ns").(*recorder)
	require.True(t, ok)

	assert.Same(t, a.m, b.m, "recorders should share one metrics registry")
	assert.Equal(t, "set-a", a.scaleSetLabels[labelKeyRunnerScaleSetName])
	assert.Equal(t, "set-b", b.scaleSetLabels[labelKeyRunnerScaleSetName])
	assert.Equal(t, "org", a.scaleSetLabels[labelKeyOrganization])
	assert.Equal(t, "ns", b.scaleSetLabels[labelKeyRunnerScaleSetNamespace])

	// Recording on each recorder must not panic and must target the shared gauges.
	a.RecordStatic(0, 3)
	b.RecordStatic(1, 9)
}
