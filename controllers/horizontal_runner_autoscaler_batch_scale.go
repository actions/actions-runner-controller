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

type batchScaleOperation struct {
	namespacedName types.NamespacedName
	scaleOps       []scaleOperation
}

type scaleOperation struct {
	trigger v1alpha1.ScaleUpTrigger
	log     logr.Logger
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

		log := s.Log

		go func() {
			log.Info("Starting batch worker")
			defer log.Info("Stopped batch worker")

			for {
				select {
				case <-s.Ctx.Done():
					return
				default:
				}

				log.V(2).Info("Batch worker is dequeueing operations")

				batches := map[types.NamespacedName]batchScaleOperation{}
				after := time.After(s.interval)
				var ops uint

			batch:
				for {
					select {
					case <-after:
						break batch
					case st := <-s.queue:
						nsName := types.NamespacedName{
							Namespace: st.HorizontalRunnerAutoscaler.Namespace,
							Name:      st.HorizontalRunnerAutoscaler.Name,
						}
						b, ok := batches[nsName]
						if !ok {
							b = batchScaleOperation{
								namespacedName: nsName,
							}
						}
						b.scaleOps = append(b.scaleOps, scaleOperation{
							log:     *st.log,
							trigger: st.ScaleUpTrigger,
						})
						batches[nsName] = b
						ops++
					}
				}

				log.V(2).Info("Batch worker dequeued operations", "ops", ops, "batches", len(batches))

			retry:
				for i := 0; ; i++ {
					failed := map[types.NamespacedName]batchScaleOperation{}

					for nsName, b := range batches {
						b := b
						if err := s.batchScale(context.Background(), b); err != nil {
							log.V(2).Info("Failed to scale due to error", "error", err)
							failed[nsName] = b
						} else {
							log.V(2).Info("Successfully ran batch scale", "hra", b.namespacedName)
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

func (s *batchScaler) batchScale(ctx context.Context, batch batchScaleOperation) error {
	var hra v1alpha1.HorizontalRunnerAutoscaler

	if err := s.Client.Get(ctx, batch.namespacedName, &hra); err != nil {
		return err
	}

	copy := hra.DeepCopy()

	copy.Spec.CapacityReservations = getValidCapacityReservations(copy)

	var added, completed int

	for _, scale := range batch.scaleOps {
		amount := 1

		if scale.trigger.Amount != 0 {
			amount = scale.trigger.Amount
		}

		scale.log.V(2).Info("Adding capacity reservation", "amount", amount)

		if amount > 0 {
			now := time.Now()
			copy.Spec.CapacityReservations = append(copy.Spec.CapacityReservations, v1alpha1.CapacityReservation{
				EffectiveTime:  metav1.Time{Time: now},
				ExpirationTime: metav1.Time{Time: now.Add(scale.trigger.Duration.Duration)},
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
		fmt.Sprintf("Patching hra %s for capacityReservations update", hra.Name),
		"before", before,
		"expired", expired,
		"added", added,
		"completed", completed,
		"after", after,
	)

	if err := s.Client.Patch(ctx, copy, client.MergeFrom(&hra)); err != nil {
		return fmt.Errorf("patching horizontalrunnerautoscaler to add capacity reservation: %w", err)
	}

	return nil
}
