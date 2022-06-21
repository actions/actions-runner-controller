package controllers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type batchScaler struct {
	Ctx      context.Context
	Client   client.Client
	Log      logr.Logger
	interval time.Duration

	queue       chan *ScaleTarget
	workerStart sync.Once
}

func newBatchScaler(ctx context.Context, client client.Client, log logr.Logger) *batchScaler {
	return &batchScaler{
		Ctx:      ctx,
		Client:   client,
		Log:      log,
		interval: 3 * time.Second,
	}
}

type scaleOperation struct {
	namespacedName types.NamespacedName
	triggers       []v1alpha1.ScaleUpTrigger
}

// Add the scale target to the unbounded queue, blocking until the target is successfully added to the queue.
// All the targets in the queue are dequeued every 3 seconds, grouped by the HRA, and applied.
// In a happy path, batchScaler update each HRA only once, even though the HRA had two or more associated webhook events in the 3 seconds interval,
// which results in less K8s API calls and less HRA update conflicts in case your ARC installation receives a lot of webhook events
func (s *batchScaler) Add(st *ScaleTarget) {
	if st == nil {
		return
	}

	s.workerStart.Do(func() {
		var expBackoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}

		s.queue = make(chan *ScaleTarget)

		go func() {
			for {
				if _, done := <-s.Ctx.Done(); done {
					return
				}

				batches := map[types.NamespacedName]scaleOperation{}
				after := time.After(s.interval)

			batch:
				for {
					select {
					case <-after:
						after = nil
						break batch
					case st := <-s.queue:
						nsName := types.NamespacedName{
							Namespace: st.HorizontalRunnerAutoscaler.Namespace,
							Name:      st.HorizontalRunnerAutoscaler.Name,
						}
						b, ok := batches[nsName]
						if !ok {
							b = scaleOperation{
								namespacedName: nsName,
							}
						}
						b.triggers = append(b.triggers, st.ScaleUpTrigger)
						batches[nsName] = b
					}
				}

			retry:
				for i := 0; ; i++ {
					failed := map[types.NamespacedName]scaleOperation{}

					for nsName, b := range batches {
						b := b
						if err := s.batchScale(context.Background(), b); err != nil {
							st.log.Error(err, "Could not scale due to %v", err)
							failed[nsName] = b
						}
					}

					if len(failed) == 0 {
						break retry
					}

					batches = failed

					delay := 16 * time.Second
					if i < len(expBackoff) {
						delay = expBackoff[i]
					}
					time.Sleep(delay)
				}
			}
		}()
	})

	s.queue <- st
}

func (s *batchScaler) batchScale(ctx context.Context, op scaleOperation) error {
	var hra v1alpha1.HorizontalRunnerAutoscaler

	if err := s.Client.Get(ctx, op.namespacedName, &hra); err != nil {
		return err
	}

	copy := hra.DeepCopy()

	copy.Spec.CapacityReservations = getValidCapacityReservations(copy)

	var added, completed int

	for _, trigger := range op.triggers {
		amount := 1

		if trigger.Amount != 0 {
			amount = trigger.Amount
		}

		if amount > 0 {
			now := time.Now()
			copy.Spec.CapacityReservations = append(copy.Spec.CapacityReservations, v1alpha1.CapacityReservation{
				EffectiveTime:  metav1.Time{Time: now},
				ExpirationTime: metav1.Time{Time: now.Add(trigger.Duration.Duration)},
				Replicas:       amount,
			})

			added += amount
		} else if amount < 0 {
			var reservations []v1alpha1.CapacityReservation

			var found bool

			for _, r := range copy.Spec.CapacityReservations {
				if !found && r.Replicas+amount == 0 {
					found = true
				} else {
					reservations = append(reservations, r)
				}
			}

			copy.Spec.CapacityReservations = reservations

			completed += amount
		}
	}

	before := len(hra.Spec.CapacityReservations)
	expired := before - len(copy.Spec.CapacityReservations)
	after := len(copy.Spec.CapacityReservations)

	s.Log.V(1).Info(
		fmt.Sprintf("Updating hra %s for capacityReservations update", hra.Name),
		"before", before,
		"expired", expired,
		"added", added,
		"completed", completed,
		"after", after,
	)

	if err := s.Client.Update(ctx, copy); err != nil {
		return fmt.Errorf("patching horizontalrunnerautoscaler to add capacity reservation: %w", err)
	}

	return nil
}
