package scaler

import (
	"sync"

	"github.com/actions/scaleset"
)

// jobQueue is an unbounded FIFO of job started events waiting to be patched.
//
// It is deliberately unbounded. The alternative, a buffered channel, blocks the
// producer once it fills, and the producer here is Scale: blocking it would put
// the job patches back on the critical path of the next scale decision, which is
// the entire reason they were moved off it.
//
// Unbounded is safe because the queue is not what decides how much work arrives.
// The scaler patches two calls per job started event, so the drain rate it needs
// is twice the rate at which jobs actually start, and that is a property of the
// workload rather than of how fast the listener polls. A faster listener polls
// more often and carries proportionally fewer events per message. The queue
// therefore sits near empty whenever the job client's QPS can sustain the real
// job start rate, and grows only when it genuinely cannot, where the backlog is
// a symptom to observe rather than something a bound would fix.
type jobQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []*scaleset.JobStarted
	closed bool
}

func newJobQueue() *jobQueue {
	q := &jobQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends events to the queue and never blocks. Events pushed after the
// queue is closed are dropped, which only happens during shutdown, once the
// workers are already on their way out and could not patch them anyway.
func (q *jobQueue) push(items ...*scaleset.JobStarted) {
	if len(items) == 0 {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}

	q.items = append(q.items, items...)
	q.cond.Broadcast()
}

// pop returns the next event, blocking until one is available. It reports false
// once the queue is both closed and fully drained, which is the signal for a
// worker to exit. Closing does not discard what is already queued, so a shutdown
// still patches everything it accepted.
func (q *jobQueue) pop() (*scaleset.JobStarted, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}

	if len(q.items) == 0 {
		return nil, false
	}

	item := q.items[0]
	// Clear the slot before reslicing so the popped event is not kept alive by
	// the backing array until it is overwritten.
	q.items[0] = nil
	q.items = q.items[1:]

	return item, true
}

// close stops the queue from accepting new events and wakes every worker so the
// ones with nothing left to do can exit.
func (q *jobQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.closed = true
	q.cond.Broadcast()
}

// depth is the number of events still waiting to be patched. It is the signal
// that the job client's QPS is not keeping up with the rate jobs are starting.
func (q *jobQueue) depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return len(q.items)
}
