package config

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1/appconfig"
	"github.com/stretchr/testify/assert"
)

func TestEffectiveScaleSetsLegacy(t *testing.T) {
	config := &Config{
		EphemeralRunnerSetName: "deployment",
		RunnerScaleSetID:       7,
		RunnerScaleSetName:     "my-set",
		MinRunners:             1,
		MaxRunners:             5,
	}

	got := config.EffectiveScaleSets()
	assert.Len(t, got, 1, "empty ScaleSets should yield exactly one synthetic entry")
	assert.Equal(t, ScaleSetConfig{
		RunnerScaleSetID:       7,
		RunnerScaleSetName:     "my-set",
		EphemeralRunnerSetName: "deployment",
		MinRunners:             1,
		MaxRunners:             5,
	}, got[0])
}

func TestEffectiveScaleSetsMulti(t *testing.T) {
	config := &Config{
		// scalar fields must be ignored when ScaleSets is populated
		RunnerScaleSetID: 999,
		ScaleSets: []ScaleSetConfig{
			{RunnerScaleSetID: 1, EphemeralRunnerSetName: "ers-a", MinRunners: 0, MaxRunners: 3},
			{RunnerScaleSetID: 2, EphemeralRunnerSetName: "ers-b", MinRunners: 1, MaxRunners: 9},
		},
	}

	got := config.EffectiveScaleSets()
	assert.Len(t, got, 2)
	assert.Equal(t, 1, got[0].RunnerScaleSetID)
	assert.Equal(t, "ers-b", got[1].EphemeralRunnerSetName)
}

func TestConfigValidationScaleSets(t *testing.T) {
	base := func() *Config {
		return &Config{
			ConfigureURL:                "https://github.com/actions",
			EphemeralRunnerSetNamespace: "namespace",
			AppConfig:                   &appconfig.AppConfig{Token: "asdf"},
		}
	}

	t.Run("valid list", func(t *testing.T) {
		c := base()
		c.ScaleSets = []ScaleSetConfig{
			{RunnerScaleSetID: 1, EphemeralRunnerSetName: "ers-a", MinRunners: 0, MaxRunners: 3},
			{RunnerScaleSetID: 2, EphemeralRunnerSetName: "ers-b", MinRunners: 1, MaxRunners: 9},
		}
		assert.NoError(t, c.Validate())
	})

	t.Run("missing scale set id", func(t *testing.T) {
		c := base()
		c.ScaleSets = []ScaleSetConfig{{EphemeralRunnerSetName: "ers-a", MaxRunners: 1}}
		assert.ErrorContains(t, c.Validate(), "ScaleSets[0].RunnerScaleSetId")
	})

	t.Run("missing ephemeral runner set name", func(t *testing.T) {
		c := base()
		c.ScaleSets = []ScaleSetConfig{{RunnerScaleSetID: 1, MaxRunners: 1}}
		assert.ErrorContains(t, c.Validate(), "ScaleSets[0].EphemeralRunnerSetName is missing")
	})

	t.Run("min greater than max", func(t *testing.T) {
		c := base()
		c.ScaleSets = []ScaleSetConfig{{RunnerScaleSetID: 1, EphemeralRunnerSetName: "ers-a", MinRunners: 5, MaxRunners: 2}}
		assert.ErrorContains(t, c.Validate(), `ScaleSets[0].MinRunners "5" cannot be greater than MaxRunners "2"`)
	})

	t.Run("duplicate ephemeral runner set name", func(t *testing.T) {
		c := base()
		c.ScaleSets = []ScaleSetConfig{
			{RunnerScaleSetID: 1, EphemeralRunnerSetName: "dup", MaxRunners: 1},
			{RunnerScaleSetID: 2, EphemeralRunnerSetName: "dup", MaxRunners: 1},
		}
		assert.ErrorContains(t, c.Validate(), `ScaleSets[1].EphemeralRunnerSetName "dup" is duplicated`)
	})

	t.Run("list mode does not require scalar scale set id", func(t *testing.T) {
		// RunnerScaleSetID is 0 here; validation must not complain because
		// ScaleSets carries the real ids.
		c := base()
		c.ScaleSets = []ScaleSetConfig{{RunnerScaleSetID: 1, EphemeralRunnerSetName: "ers-a", MaxRunners: 1}}
		assert.NoError(t, c.Validate())
	})
}
