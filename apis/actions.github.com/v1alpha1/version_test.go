package v1alpha1_test

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestIsVersionAllowed(t *testing.T) {
	t.Parallel()
	tt := map[string]struct {
		resourceVersion string
		buildVersion    string
		want            bool
	}{
		"dev should always be allowed": {
			resourceVersion: "0.11.0",
			buildVersion:    "dev",
			want:            true,
		},
		"resourceVersion is not semver": {
			resourceVersion: "dev",
			buildVersion:    "0.11.0",
			want:            false,
		},
		"buildVersion is not semver": {
			resourceVersion: "0.11.0",
			buildVersion:    "NA",
			want:            false,
		},
		"major version mismatch": {
			resourceVersion: "0.11.0",
			buildVersion:    "1.11.0",
			want:            false,
		},
		"minor version mismatch": {
			resourceVersion: "0.11.0",
			buildVersion:    "0.10.0",
			want:            false,
		},
		"patch version mismatch": {
			resourceVersion: "0.11.1",
			buildVersion:    "0.11.0",
			want:            true,
		},
		"arbitrary version match": {
			resourceVersion: "abc",
			buildVersion:    "abc",
			want:            true,
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			got := v1alpha1.IsVersionAllowed(tc.resourceVersion, tc.buildVersion)
			assert.Equal(t, tc.want, got)
		})
	}
}
