package controllers

import (
	"context"
)

// worker is a worker that has a non-blocking bounded queue of scale targets, dequeues scale target and executes the scale operation one by one.
type worker struct {
	scaleTargetQueue chan *ScaleTarget
	work             func(*ScaleTarget)
	done             chan struct{}
}

func newWorker(ctx context.Context, queueLimit int, work func(*ScaleTarget)) *worker {
	w := &worker{
		scaleTargetQueue: make(chan *ScaleTarget, queueLimit),
		work:             work,
		done:             make(chan struct{}),
	}

	go func() {
		defer close(w.done)

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-w.scaleTargetQueue:
				work(t)
			}
		}
	}()

	return w
}

// Add the scale target to the bounded queue, returning the result as a bool value. It returns true on successful enqueue, and returns false otherwise.
// When returned false, the queue is already full so the enqueue operation must be retried later.
// If the enqueue was triggered by an external source and there's no intermediate queue that we can use,
// you must instruct the source to resend the original request later.
// In case you're building a webhook server around this worker, this means that you must return a http error to the webhook server,
// so that (hopefully) the sender can resend the webhook event later, or at least the human operator can notice or be notified about the
// webhook develiery failure so that a manual retry can be done later.
func (w *worker) Add(st *ScaleTarget) bool {
	select {
	case w.scaleTargetQueue <- st:
		return true
	default:
		return false
	}
}

func (w *worker) Done() chan struct{} {
	return w.done
}
