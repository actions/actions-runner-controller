package actionsgithubcom

import (
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// goldenARS returns a fixed AutoscalingRunnerSet with NO runnerVariants set, i.e.
// the legacy single-set shape. The child object hashes it produces must never
// change when runnerVariants is added to the API, so an in-place upgrade of the
// controller does not churn every existing scale set's listener pod, RBAC, or
// config secret. The constants below were captured from the pre-runnerVariants
// HEAD (gha-runner-scale-set-0.14.2-19-ga035c5a); if this test fails, the empty
// path is no longer byte-identical and the change is not a safe upgrade.
func goldenARS() *v1alpha1.AutoscalingRunnerSet {
	minRunners := 1
	maxRunners := 5
	return &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "golden-scale-set",
			Namespace: "golden-ns",
			Labels: map[string]string{
				LabelKeyKubernetesPartOf:  labelValueKubernetesPartOf,
				LabelKeyKubernetesVersion: "0.14.2",
			},
			Annotations: map[string]string{
				runnerScaleSetIDAnnotationKey:         "7",
				AnnotationKeyGitHubRunnerGroupName:    "golden-group",
				AnnotationKeyGitHubRunnerScaleSetName: "golden-scale-set",
			},
		},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl:    "https://github.com/golden-org/golden-repo",
			GitHubConfigSecret: "golden-secret",
			RunnerGroup:        "golden-group",
			RunnerScaleSetName: "golden-scale-set",
			MinRunners:         &minRunners,
			MaxRunners:         &maxRunners,
		},
	}
}

func TestBackCompatGoldenHashes(t *testing.T) {
	b := ResourceBuilder{}
	ars := goldenARS()

	ers, err := b.newEphemeralRunnerSet(ars)
	require.NoError(t, err)
	listener, err := b.newAutoscalingListener(ars, ers, ars.Namespace, "ghcr.io/actions/gha-runner-scale-set-controller:0.14.2", nil)
	require.NoError(t, err)
	role := b.newScaleSetListenerRole(listener)

	// Captured from pre-runnerVariants HEAD. Do NOT update these to make the test
	// pass after a change: a diff here means existing scale sets would churn on
	// upgrade, which is exactly what runnerVariants must avoid.
	const (
		goldenERSIntegrityHash = "7b946d778f"
		goldenListenerSpecHash = "7bc7d69f44"
		goldenRoleHash         = "6c65c546c"
	)

	// During baseline capture these print the live values; the constants are then
	// pinned. Kept as t.Logf so a future maintainer can re-capture if the
	// hash algorithm itself is intentionally changed.
	t.Logf("ERS integrity hash    = %q", ers.Annotations[annotationKeyIntegrityHash])
	t.Logf("listener spec hash    = %q", listener.Annotations[annotationKeyIntegrityHash])
	t.Logf("role integrity hash   = %q", role.Annotations[annotationKeyIntegrityHash])

	require.Equal(t, goldenERSIntegrityHash, ers.Annotations[annotationKeyIntegrityHash], "ERS integrity hash drifted; existing scale sets would recreate their runner set on upgrade")
	require.Equal(t, goldenListenerSpecHash, listener.Annotations[annotationKeyIntegrityHash], "listener spec hash drifted; existing scale sets would recreate their listener pod on upgrade")
	require.Equal(t, goldenRoleHash, role.Annotations[annotationKeyIntegrityHash], "listener Role hash drifted; existing scale sets would recreate their RBAC on upgrade")

	// RunnerSetSpecHash drives EphemeralRunner recreation. It hashes an explicit
	// subset of the spec that does not include runnerVariants, so it must stay
	// stable; a drift here would recreate every running runner pod on upgrade.
	const goldenRunnerSetSpecHash = "744845c56d"
	t.Logf("runner set spec hash  = %q", ars.RunnerSetSpecHash())
	require.Equal(t, goldenRunnerSetSpecHash, ars.RunnerSetSpecHash(), "RunnerSetSpecHash drifted; existing runner pods would be recreated on upgrade")
}

// The AutoscalingRunnerSet level Hash and ListenerSpecHash DO change once when
// runnerVariants is added to the spec (spew includes the new nil field). That is
// the accepted one-time rehash: it flips the AutoscalingRunnerSet through Pending
// on the first reconcile after upgrade but, as TestBackCompatGoldenHashes proves,
// churns no child object. This test pins the post-change values so the one-time
// nature stays visible and any further drift is caught.
func TestARSLevelHashChangedOnceOnly(t *testing.T) {
	ars := goldenARS()
	t.Logf("ARS.Hash()          = %q", ars.Hash())
	t.Logf("ARS.ListenerSpecHash= %q", ars.ListenerSpecHash())
	const (
		arsHash         = "6648ffdc9"
		listenerSpecArs = "c5ff4646"
	)
	require.Equal(t, arsHash, ars.Hash(), "AutoscalingRunnerSet.Hash changed unexpectedly beyond the accepted one-time rehash")
	require.Equal(t, listenerSpecArs, ars.ListenerSpecHash(), "AutoscalingRunnerSet.ListenerSpecHash changed unexpectedly beyond the accepted one-time rehash")
}
