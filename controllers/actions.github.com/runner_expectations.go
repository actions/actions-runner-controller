package actionsgithubcom

import (
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const runnerExpectationsTimeout = 5 * time.Minute
const runnerExpectationsRetry = time.Second

// runnerExpectations bridges successful writes and their observation in the
// informer cache. Reconciles for one set are serialized, but different sets and
// the workers within a runner batch can record writes concurrently. Its zero
// value is ready to use.
type runnerExpectations struct {
	mu      sync.Mutex
	pending map[types.NamespacedName]*pendingRunnerChanges
}

type pendingRunnerChanges struct {
	uid     types.UID
	since   time.Time
	creates map[string]struct{}
	deletes map[string]struct{}
}

func (e *runnerExpectations) record(key types.NamespacedName, uid types.UID, name string, deleting bool, now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.pending == nil {
		e.pending = make(map[types.NamespacedName]*pendingRunnerChanges)
	}
	p := e.pending[key]
	if p == nil || p.uid != uid {
		p = &pendingRunnerChanges{uid: uid, since: now, creates: make(map[string]struct{}), deletes: make(map[string]struct{})}
		e.pending[key] = p
	}
	if deleting {
		p.deletes[name] = struct{}{}
	} else {
		p.creates[name] = struct{}{}
	}
}

func (e *runnerExpectations) created(set *v1alpha1.EphemeralRunnerSet, name string, now time.Time) {
	e.record(types.NamespacedName{Namespace: set.Namespace, Name: set.Name}, set.UID, name, false, now)
}

func (e *runnerExpectations) deleted(runner *v1alpha1.EphemeralRunner, now time.Time) {
	owner := metav1.GetControllerOfNoCopy(runner)
	if owner == nil || owner.APIVersion != v1alpha1.GroupVersion.String() || owner.Kind != "EphemeralRunnerSet" {
		return
	}
	e.record(types.NamespacedName{Namespace: runner.Namespace, Name: owner.Name}, owner.UID, runner.Name, true, now)
}

// observe prunes individual names as the cache catches up. A terminating runner
// has left the active scale-down count, but still occupies a scale-up slot, so
// its deletionTimestamp is enough to observe a delete without waiting for its
// finalizers. The caller logs expiry, which deliberately trades stale-cache
// protection for progress after a lost observation.
func (e *runnerExpectations) observe(set *v1alpha1.EphemeralRunnerSet, runners *v1alpha1.EphemeralRunnerList, now time.Time) (satisfied, expired bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := types.NamespacedName{Namespace: set.Namespace, Name: set.Name}
	p := e.pending[key]
	if p == nil {
		return true, false
	}
	if p.uid != set.UID {
		delete(e.pending, key)
		return true, false
	}
	// Only names of our writes are relevant; no namespace-wide API read or
	// informer event handler is needed.
	present := make(map[string]bool, len(p.deletes))
	for i := range runners.Items {
		runner := &runners.Items[i]
		delete(p.creates, runner.Name)
		if _, pending := p.deletes[runner.Name]; pending {
			present[runner.Name] = runner.DeletionTimestamp.IsZero()
		}
	}
	for name := range p.deletes {
		if !present[name] {
			delete(p.deletes, name)
		}
	}
	if len(p.creates) == 0 && len(p.deletes) == 0 {
		delete(e.pending, key)
		return true, false
	}
	if now.Sub(p.since) >= runnerExpectationsTimeout {
		delete(e.pending, key)
		return true, true
	}
	return false, false
}

func (e *runnerExpectations) hasPending(key types.NamespacedName) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.pending[key] != nil
}

func (e *runnerExpectations) clear(key types.NamespacedName) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.pending, key)
}
