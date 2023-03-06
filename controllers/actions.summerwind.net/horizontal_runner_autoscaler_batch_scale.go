package actionssummerwindnet

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
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

		now := time.Now()
		if amount > 0 {
			copy.Spec.CapacityReservations = append(copy.Spec.CapacityReservations, v1alpha1.CapacityReservation{
				EffectiveTime:  metav1.Time{Time: now},
				ExpirationTime: metav1.Time{Time: now.Add(scale.trigger.Duration.Duration)},
				Replicas:       amount,
			})

			added += amount
		} else if amount < 0 {
			var reservations []v1alpha1.CapacityReservation

			var (
				found    bool
				foundIdx int
			)

			for i, r := range copy.Spec.CapacityReservations {
				r := r
				if !found && r.Replicas+amount == 0 {
					found = true
					foundIdx = i
				} else {
					// Note that we nil-check max replicas because this "fix" is needed only when there is the upper limit of runners.
					// In other words, you don't need to reset effective time and expiration time when there is no max replicas.
					// That's because the desired replicas would already contain the reservation since it's creation.
					if found && copy.Spec.MaxReplicas != nil && i > foundIdx+*copy.Spec.MaxReplicas {
						// Update newer CapacityReservations' time to now to trigger reconcile
						// Without this, we might stuck in minReplicas unnecessarily long.
						// That is, we might not scale up after an ephemeral runner has been deleted
						// until a new scale up, all runners finish, or after DefaultRunnerPodRecreationDelayAfterWebhookScale
						// See https://github.com/actions/actions-runner-controller/issues/2254 for more context.
						r.EffectiveTime = metav1.Time{Time: now}

						// We also reset the scale trigger expiration time, so that you don't need to tweak
						// scale trigger duratoin depending on maxReplicas.
						// A detailed explanation follows.
						//
						// Let's say maxReplicas=3 and the workflow job of status=canceled result in deleting the first capacity reservation hence i=0.
						// We are interested in at least four reservations and runners:
						//   i=0   - already included in the current desired replicas, but just got deleted
						//   i=1-2 - already included in the current desired replicas
						//   i=3   - not yet included in the current desired replicas, might have been expired while waiting in the queue
						//
						// i=3 is especially important here- If we didn't reset the expiration time of 3rd reservation,
						// it might expire before a corresponding runner is created, due to the delay between the expiration timer starts and the runner is created.
						//
						// Why is there such delay? Because ARC implements the scale duration and expiration as such...
						// The expiration timer starts when the reservation is created, while the runner is created only after the corresponding reservation fits within maxReplicas.
						//
						// We address that, by resetting the expiration time for fourth(i=3 in the above example) and subsequent reservations when the first reservation gets cancelled.
						r.ExpirationTime = metav1.Time{Time: now.Add(scale.trigger.Duration.Duration)}
					}

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
