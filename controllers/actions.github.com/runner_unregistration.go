/*
Copyright 2020 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actionsgithubcom

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	// unregistrationRetryDelay is how long a request waits before it is attempted
	// again after the service reported that the runner is still executing a job.
	unregistrationRetryDelay = 30 * time.Second

	// unregistrationMinWorkers is the lower bound on the size of the worker pool.
	unregistrationMinWorkers = 4

	// unregistrationMaxIdleWait bounds how long a worker sleeps when the queue
	// holds nothing it can act on yet. Pushes wake workers directly, so this is
	// only a backstop against a wake-up that was coalesced away.
	unregistrationMaxIdleWait = 30 * time.Second
)

// runnerSelfDeregistered reports whether the runner removed its own
// registration from the Actions service before its EphemeralRunner was deleted.
//
// It is an inference drawn from state the controller already has, not a lookup,
// because the whole point is to avoid the API call. It is deliberately
// conservative in the direction that costs an API call rather than the one that
// leaks a registration.
//
// The Succeeded phase is set from a single observation: the runner container
// terminated with exit code 0. Runners are configured as ephemeral, so a runner
// that reaches a clean exit has already removed its own registration on the way
// out, and asking the service to remove it again is a wasted round trip on the
// hottest path the controller has.
//
// Every other phase is reachable with the registration still in place. A runner
// that never started, was killed, exited non-zero, or was found to be outdated
// did not get to deregister itself.
func runnerSelfDeregistered(ephemeralRunner *v1alpha1.EphemeralRunner) bool {
	return ephemeralRunner.Status.Phase == v1alpha1.EphemeralRunnerPhaseSucceeded
}

// runnerUnregistration is a single queued attempt to remove one runner from the
// Actions service.
type runnerUnregistration struct {
	// runner is a copy of the EphemeralRunner taken before it was deleted from
	// the cluster. The workers hold the whole object rather than just the runner
	// ID because resolving the credentials of the scale set the runner belongs
	// to needs its GitHub configuration, and by the time a worker gets to it the
	// object is usually gone from the API server.
	runner *v1alpha1.EphemeralRunner

	// runnerID identifies the registration to remove. It is carried separately
	// because it is not always the one the runner's status reports: a runner
	// deleted before the controller recorded the ID has its registration
	// identified from the jitconfig secret instead.
	runnerID int

	// readyAt is the earliest time this request may be attempted. The zero value
	// means it is ready immediately.
	readyAt time.Time
}

// key identifies the registration this request is trying to remove.
//
// The runner ID alone would be the obvious key, but it is assigned by the
// Actions service and only unique within the scale set's GitHub scope. One
// controller can serve several of those, so the runner is named as well.
func (r runnerUnregistration) key() unregistrationKey {
	return unregistrationKey{
		namespace: r.runner.Namespace,
		name:      r.runner.Name,
		runnerID:  r.runnerID,
	}
}

type unregistrationKey struct {
	namespace string
	name      string
	runnerID  int
}

// RunnerUnregistrationQueue removes runners from the Actions service outside of
// the EphemeralRunner reconcile loop.
//
// Finalizing an EphemeralRunner is two unrelated pieces of work: deleting the
// Kubernetes resources it owns, and removing its registration from the Actions
// service. Only the first is local and fast. Doing them in sequence puts pod
// garbage collection behind an external, eventually consistent API, so a burst
// of completing jobs leaves completed pods standing around waiting on calls
// they have no real dependency on.
//
// So the reconciler hands the registration over to this queue and moves on.
// Push appends to a slice under a mutex and returns; a fixed pool of workers
// issues the API calls. Nothing the Actions service does can extend a reconcile.
//
// The queue is in-memory only, and deliberately so. Requests still queued when
// the controller restarts or loses leader election are dropped, and nothing
// recovers them. That is acceptable because this is not the only thing that
// removes a runner: the Actions service drops a registration on its own once
// the runner stops reporting in, so a lost request delays that cleanup rather
// than leaking the registration. Making the queue durable would mean writing to
// the API server for every deletion, which is the cost this type exists to
// avoid.
//
// A zero value is not usable. Use NewRunnerUnregistrationQueue.
type RunnerUnregistrationQueue struct {
	log            logr.Logger
	secretResolver SecretResolver
	workers        int
	retryDelay     time.Duration

	mu sync.Mutex

	// ready holds requests that can be attempted now, oldest first. It is
	// consumed through readyHead rather than by resliding from the front,
	// because a burst of deletions arrives all at once and shifting the tail
	// down on every take would make draining it quadratic, under the lock that
	// Push needs.
	ready     []runnerUnregistration
	readyHead int

	// delayed holds requests waiting out a retry delay. Only a runner the
	// service reported as still executing a job ends up here, so it stays short
	// enough to scan on every take.
	delayed []runnerUnregistration

	// claimed holds the key of every request that has been taken on and not yet
	// finished with, whether it is waiting in the queue or in the middle of an
	// API call. Pushing a key that is already in here drops the request, so the
	// service is never asked twice to remove the same registration.
	//
	// A retry keeps its claim, which is what this mainly protects: two copies of
	// a request for a runner that is still executing a job would sit in the
	// delayed list retrying in lockstep for as long as the job runs.
	claimed map[unregistrationKey]struct{}

	// notify carries a single wake-up token for a waiting worker. It is only
	// ever sent to without blocking, so that a push is never slowed down by the
	// state of the pool.
	notify chan struct{}
}

// The manager owns the lifecycle of the pool.
var _ manager.Runnable = (*RunnerUnregistrationQueue)(nil)

// NewRunnerUnregistrationQueue returns a queue drained by a pool of workers
// goroutines, never fewer than unregistrationMinWorkers. The controller
// defaults to a single concurrent reconcile, and sizing the pool off that alone
// would leave one goroutine to absorb every burst.
func NewRunnerUnregistrationQueue(log logr.Logger, secretResolver SecretResolver, workers int) *RunnerUnregistrationQueue {
	return &RunnerUnregistrationQueue{
		log:            log,
		secretResolver: secretResolver,
		workers:        max(workers, unregistrationMinWorkers),
		retryDelay:     unregistrationRetryDelay,
		claimed:        make(map[unregistrationKey]struct{}),
		notify:         make(chan struct{}, 1),
	}
}

// Push queues the removal of runnerID, the registration held by the given
// runner, from the Actions service.
//
// It copies what the workers need and returns. It never blocks, never fails and
// never touches the network, so no caller can be delayed by how far behind the
// pool is or by how the service is behaving.
//
// A registration that is already queued, or is in the middle of being removed,
// is not queued again, so the service is asked to remove it exactly once.
//
// Pushing to a nil queue drops the request. The only way to get one is to build
// an EphemeralRunnerReconciler by hand and never call SetupWithManager, which
// wires a queue up when the field is left unset.
func (q *RunnerUnregistrationQueue) Push(ephemeralRunner *v1alpha1.EphemeralRunner, runnerID int) {
	if q == nil {
		return
	}
	request := runnerUnregistration{runner: ephemeralRunner.DeepCopy(), runnerID: runnerID}
	if !q.push(request) {
		q.log.V(1).Info(
			"Runner is already queued for removal from the service",
			"ephemeralRunner", types.NamespacedName{Namespace: ephemeralRunner.Namespace, Name: ephemeralRunner.Name},
			"runnerId", runnerID,
		)
	}
}

// Start drains the queue until ctx is cancelled.
//
// It implements manager.Runnable so the pool shares the manager's lifecycle,
// starting alongside the controllers and stopping when the manager shuts down.
// Whatever is still queued at that point is dropped; see the type
// documentation for why that is safe.
func (q *RunnerUnregistrationQueue) Start(ctx context.Context) error {
	q.log.Info("Starting runner unregistration workers", "workers", q.workers)

	var wg sync.WaitGroup
	wg.Add(q.workers)
	for range q.workers {
		go func() {
			defer wg.Done()
			q.work(ctx)
		}()
	}
	wg.Wait()

	q.log.Info("Stopped runner unregistration workers", "dropped", q.len())
	return nil
}

// work is the loop of a single worker. It takes whatever is ready, and when
// nothing is, waits for the shorter of the next request becoming ready and the
// next push.
func (q *RunnerUnregistrationQueue) work(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		request, wait, ok := q.next(time.Now())
		if ok {
			q.unregister(ctx, request)
			continue
		}

		// Nothing is ready, so park until something is. wait is how long the
		// earliest delayed request still has to go, and it is an upper bound
		// rather than a commitment: a push cuts it short. That is what keeps a
		// request needing no wait from queueing behind ones that do, even when
		// every worker is parked on a retry 30 seconds out.
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-q.notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// unregister issues the removal for a single request.
//
// Failures are logged and dropped rather than retried, with one exception: the
// service refuses to remove a runner it still considers to be executing a job,
// and that request goes back on the queue. Everything else is left to the
// service, which removes a registration that stops reporting in on its own.
// There is no reconcile to fail here and no caller waiting on the result, so
// retrying anything else would only build a backlog of calls that the service
// is already going to make unnecessary.
func (q *RunnerUnregistrationQueue) unregister(ctx context.Context, request runnerUnregistration) {
	runner := request.runner
	log := q.log.WithValues(
		"ephemeralRunner", types.NamespacedName{Namespace: runner.Namespace, Name: runner.Name},
		"runnerId", request.runnerID,
	)

	// The claim taken when this was queued is held until the request is done
	// with, so nothing can queue the same registration alongside it. A retry is
	// not done with, and keeps the claim.
	retrying := false
	defer func() {
		if !retrying {
			q.release(request)
		}
	}()

	actionsClient, err := q.secretResolver.GetActionsService(ctx, runner)
	if err != nil {
		log.Error(err, "Failed to get actions client to remove the runner from the service; leaving the runner for the service to clean up")
		return
	}

	err = actionsClient.RemoveRunner(ctx, int64(request.runnerID))
	switch {
	case err == nil:
		log.Info("Removed runner from the service")

	case errors.Is(err, scaleset.RunnerNotFoundError), errors.Is(err, scaleset.NotFoundError):
		// The registration is gone, which is the outcome this request wanted.
		log.Info("Runner is already removed from the service")

	case errors.Is(err, scaleset.JobStillRunningError):
		log.Info("Runner is still executing a job, retrying the removal later", "retryAfter", q.retryDelay)
		retrying = true
		q.pushAfter(request, q.retryDelay)

	case ctx.Err() != nil:
		log.Info("Shutting down before the runner could be removed from the service; leaving the runner for the service to clean up")

	default:
		log.Error(err, "Failed to remove the runner from the service; leaving the runner for the service to clean up")
	}
}

// next takes the oldest request that is ready to be attempted at now.
//
// When nothing is ready it reports how long to wait before asking again, which
// is the time until the earliest delayed request, capped at
// unregistrationMaxIdleWait.
func (q *RunnerUnregistrationQueue) next(now time.Time) (runnerUnregistration, time.Duration, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	wait := q.promoteLocked(now)

	if q.readyHead == len(q.ready) {
		return runnerUnregistration{}, wait, false
	}

	request := q.ready[q.readyHead]
	// Drop the reference so that the consumed entry does not keep the copy of
	// the runner alive until the slice is reused.
	q.ready[q.readyHead] = runnerUnregistration{}
	q.readyHead++
	if q.readyHead == len(q.ready) {
		q.ready = q.ready[:0]
		q.readyHead = 0
	} else if q.readyHead >= len(q.ready)-q.readyHead {
		remaining := len(q.ready) - q.readyHead
		copy(q.ready, q.ready[q.readyHead:])
		clear(q.ready[remaining:])
		q.ready = q.ready[:remaining]
		q.readyHead = 0
	}

	if q.readyHead < len(q.ready) || len(q.delayed) > 0 {
		// Hand the wake-up on so that a burst of pushes, which only ever leaves a
		// single token behind, spreads across the pool instead of being worked
		// through by whichever worker happened to take the first one. Waking for
		// a request that turns out to still be delayed costs one more pass
		// through this function.
		q.wake()
	}

	return request, 0, true
}

// promoteLocked moves every delayed request that has come due onto the ready
// list and returns how long to wait for the earliest of the ones that have not,
// capped at unregistrationMaxIdleWait.
func (q *RunnerUnregistrationQueue) promoteLocked(now time.Time) time.Duration {
	if len(q.delayed) == 0 {
		return unregistrationMaxIdleWait
	}

	// Partitioned in a single pass. A burst of runners refused together comes
	// due together, and removing them one at a time would shift the rest of the
	// list on every promotion, under the lock Push needs.
	wait := unregistrationMaxIdleWait
	kept := q.delayed[:0]
	for _, request := range q.delayed {
		if request.readyAt.After(now) {
			wait = min(wait, request.readyAt.Sub(now))
			kept = append(kept, request)
			continue
		}

		request.readyAt = time.Time{}
		q.ready = append(q.ready, request)
	}

	// Compacting leaves the promoted requests duplicated in the tail, where they
	// would keep their copy of the runner alive until the slice is reused.
	clear(q.delayed[len(kept):])
	q.delayed = kept

	return wait
}

// pushAfter queues request again, to be attempted no earlier than delay from
// now.
//
// The request keeps the claim it already holds, so this cannot be turned away
// as a duplicate of itself.
func (q *RunnerUnregistrationQueue) pushAfter(request runnerUnregistration, delay time.Duration) {
	request.readyAt = time.Now().Add(delay)
	q.enqueue(request)
}

// push queues a request for a registration that is not already queued, and
// reports whether it took it on.
func (q *RunnerUnregistrationQueue) push(request runnerUnregistration) bool {
	q.mu.Lock()
	if _, ok := q.claimed[request.key()]; ok {
		q.mu.Unlock()
		return false
	}
	q.claimed[request.key()] = struct{}{}
	q.mu.Unlock()

	q.enqueue(request)
	return true
}

// release gives up the claim on a request that is done with, whatever the
// outcome was. A later request to remove the same registration is then taken on
// as a new one.
func (q *RunnerUnregistrationQueue) release(request runnerUnregistration) {
	q.mu.Lock()
	delete(q.claimed, request.key())
	q.mu.Unlock()
}

func (q *RunnerUnregistrationQueue) enqueue(request runnerUnregistration) {
	q.mu.Lock()
	if request.readyAt.IsZero() {
		q.ready = append(q.ready, request)
	} else {
		q.delayed = append(q.delayed, request)
	}
	q.mu.Unlock()

	q.wake()
}

// wake releases one worker from its wait. The send is non-blocking against a
// single-slot buffer, so a push is never delayed by the pool, and a wake-up
// that nobody is waiting for is kept for whoever waits next.
func (q *RunnerUnregistrationQueue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *RunnerUnregistrationQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ready) - q.readyHead + len(q.delayed)
}
