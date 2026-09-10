package actionsgithubcom

import (
	"testing"
)

// BenchmarkListenerPodSpecRequiresRecreation measures the listener pod drift
// check, which also runs on every AutoscalingListener reconcile.
func BenchmarkListenerPodSpecRequiresRecreation(b *testing.B) {
	desired := desiredListenerPod()
	live := livePodFromDesired(desired)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if listenerPodSpecRequiresRecreation(live, desired) {
			b.Fatal("expected no recreation")
		}
	}
}
