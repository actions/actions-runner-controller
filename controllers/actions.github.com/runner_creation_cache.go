package actionsgithubcom

import (
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const runnerCacheTTL = time.Minute

// runnerCreationCache tracks successful creates until the informer observes them.
// Its zero value is ready to use.
type runnerCreationCache struct {
	// Key by namespace/name so a NotFound reconcile can remove the entry without
	// knowing its UID; lazy expiry would otherwise leave an orphaned UID-keyed entry.
	sets sync.Map // types.NamespacedName -> *runnerSetCache
}

// ClearRunnerCreationCache drops the local inventory when the manager stops leading.
// In-flight writes may still record references while the process shuts down.
func (r *EphemeralRunnerSetReconciler) ClearRunnerCreationCache() {
	r.runnerCreationCache.clear()
}

func (c *runnerCreationCache) clear() {
	c.sets.Clear()
}

func (c *runnerCreationCache) delete(key types.NamespacedName) {
	c.sets.Delete(key)
}

// Reconciles for one set are serialized, but batch workers update records concurrently.
type runnerSetCache struct {
	mu      sync.Mutex
	uid     types.UID // Parent EphemeralRunnerSet UID; isolates replacements with the same namespace/name.
	runners map[types.UID]time.Time
}

func (c *runnerCreationCache) record(set *v1alpha1.EphemeralRunnerSet, uid types.UID, now time.Time) {
	key := client.ObjectKeyFromObject(set)
	value, found := c.sets.Load(key)
	if !found {
		value, _ = c.sets.LoadOrStore(key, &runnerSetCache{})
	}
	cached := value.(*runnerSetCache)
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if cached.uid != set.UID || cached.runners == nil {
		cached.uid = set.UID
		cached.runners = make(map[types.UID]time.Time)
	}
	cached.runners[uid] = now.Add(runnerCacheTTL)
}

// shortcut: this overlay is process-local and expires after 60s; stricter
// failover or prolonged cache-lag guarantees require durable coordination.
func (c *runnerCreationCache) observe(set *v1alpha1.EphemeralRunnerSet, runners *v1alpha1.EphemeralRunnerList, now time.Time) (unseenCreates, expired int) {
	key := client.ObjectKeyFromObject(set)
	value, found := c.sets.Load(key)
	if !found {
		return 0, 0
	}
	cached := value.(*runnerSetCache)
	cached.mu.Lock()
	defer cached.mu.Unlock()
	if cached.uid != set.UID || len(cached.runners) == 0 {
		c.sets.Delete(key)
		return 0, 0
	}
	// Prune against this exact snapshot, never against asynchronous watch events.
	for i := 0; i < len(runners.Items) && len(cached.runners) > 0; i++ {
		runner := &runners.Items[i]
		if metav1.IsControlledBy(runner, set) {
			delete(cached.runners, runner.UID)
		}
	}
	for uid, expiresAt := range cached.runners {
		if !now.Before(expiresAt) {
			delete(cached.runners, uid)
			expired++
		}
	}
	unseenCreates = len(cached.runners)
	if len(cached.runners) == 0 {
		c.sets.Delete(key)
	}
	return unseenCreates, expired
}
