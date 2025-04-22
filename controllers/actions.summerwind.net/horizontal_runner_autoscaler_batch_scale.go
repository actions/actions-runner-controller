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
// In a happy path, batchScaler updates each HRA only once, even though the HRA had two or more associated webhook events in the 3 seconds interval,
// which results in fewer K8s API calls and fewer HRA update conflicts in case your ARC installation receives a lot of webhook events
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
							Namespace: st.Namespace,
							Name:      st.Name,
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

	now := time.Now()

	copy, err := s.planBatchScale(ctx, batch, &hra, now)
	if err != nil {
		return err
	}

	if err := s.Client.Patch(ctx, copy, client.MergeFrom(&hra)); err != nil {
		return fmt.Errorf("patching horizontalrunnerautoscaler to add capacity reservation: %w", err)
	}

	return nil
}

func (s *batchScaler) planBatchScale(ctx context.Context, batch batchScaleOperation, hra *v1alpha1.HorizontalRunnerAutoscaler, now time.Time) (*v1alpha1.HorizontalRunnerAutoscaler, error) {
	copy := hra.DeepCopy()

	if hra.Spec.MaxReplicas != nil && len(copy.Spec.CapacityReservations) > *copy.Spec.MaxReplicas {
		// We have more reservations than MaxReplicas, meaning that we previously
		// could not scale up to meet a capacity demand because we had hit MaxReplicas.
		// Therefore, there are reservations that are starved for capacity. We extend the
		// expiration time on these starved reservations because the "duration" is meant
		// to apply to reservations that have launched replicas, not replicas in the backlog.
		// Of course, if MaxReplicas is nil, then there is no max to hit, and we do not need this adjustment.
		// See https://github.com/actions/actions-runner-controller/issues/2254 for more context.

		// Extend the expiration time of all the reservations not yet assigned to replicas.
		//
		// Note that we assume that the two scenarios equivalent here.
		// The first case is where the number of reservations become greater than MaxReplicas.
		// The second case is where MaxReplicas become greater than the number of reservations equivalent.
		// Presuming the HRA.spec.scaleTriggers[].duration as "the duration until the reservation expires after a corresponding runner was deployed",
		// it's correct.
		//
		// In other words, we settle on a capacity reservation's ExpirationTime only after the corresponding runner is "about to be" deployed.
		// It's "about to be deployed" not "deployed" because we have no way to correlate a capacity reservation and the runner;
		// the best we can do here is to simulate the desired behavior by reading MaxReplicas and assuming it will be equal to the number of active runners soon.
		//
		// Perhaps we could use RunnerDeployment.Status.Replicas or RunnerSet.Status.Replicas instead of the MaxReplicas as a better source of "the number of active runners".
		// However, note that the status is not guaranteed to be up-to-date.
		// It might not be that easy to decide which is better to use.
		for i := *hra.Spec.MaxReplicas; i < len(copy.Spec.CapacityReservations); i++ {
			// Let's say maxReplicas=3 and the workflow job of status=completed result in deleting the first capacity reservation
			//   copy.Spec.CapacityReservations[i] where i=0.
			// We are interested in at least four reservations and runners:
			//   i=0   - already included in the current desired replicas, but may be about to be deleted
			//   i=1-2 - already included in the current desired replicas
			//   i=3   - not yet included in the current desired replicas, might have been expired while waiting in the queue
			//
			// i=3 is especially important here- If we didn't reset the expiration time of this reservation,
			// it might expire before it is assigned to a runner, due to the delay between the time the
			// expiration timer starts and the time a runner becomes available.
			//
			// Why is there such delay? Because ARC implements the scale duration and expiration as such.
			// The expiration timer starts when the reservation is created, while the runner is created only after
			// the corresponding reservation fits within maxReplicas.
			//
			// We address that, by resetting the expiration time for fourth(i=3 in the above example)
			// and subsequent reservations whenever a batch is run (which is when expired reservations get deleted).

			// There is no guarantee that all the reservations have the same duration, and even if there were,
			// at this point we have lost the reference to the duration that was intended.
			// However, we can compute the intended duration from the existing interval.
			//
			// In other words, updating HRA.spec.scaleTriggers[].duration does not result in delaying capacity reservations expiration any longer
			// than the "intended" duration, which is the duration of the trigger when the reservation was created.
			duration := copy.Spec.CapacityReservations[i].ExpirationTime.Sub(copy.Spec.CapacityReservations[i].EffectiveTime.Time)
			copy.Spec.CapacityReservations[i].EffectiveTime = metav1.Time{Time: now}
			copy.Spec.CapacityReservations[i].ExpirationTime = metav1.Time{Time: now.Add(duration)}
		}
	}

	// Now we can filter out any expired reservations from consideration.
	// This could leave us with 0 reservations left.
	copy.Spec.CapacityReservations = getValidCapacityReservations(copy)
	before := len(hra.Spec.CapacityReservations)
	expired := before - len(copy.Spec.CapacityReservations)

	var added, completed int

	for _, scale := range batch.scaleOps {
		amount := scale.trigger.Amount

		// We do not track if a webhook-based scale-down event matches an expired capacity reservation
		// or a job for which the scale-up event was never received. This means that scale-down
		// events could drive capacity reservations into the negative numbers if we let it.
		// We ensure capacity never falls below zero, but that also means that the
		// final number of capacity reservations depends on the order in which events come in.
		// If capacity is at zero and we get a scale-down followed by a scale-up,
		// the scale-down will be ignored and we will end up with a desired capacity of 1.
		// However, if we get the scale-up first, the scale-down will drive desired capacity back to zero.
		// This could be fixed by matching events' `workflow_job.run_id` with capacity reservations,
		// but that would be a lot of work. So for now we allow for some slop, and hope that
		// GitHub provides a better autoscaling solution soon.
		if amount > 0 {
			scale.log.V(2).Info("Adding capacity reservation", "amount", amount)

			// Parts of this function require that Spec.CapacityReservations.Replicas always equals 1.
			// Enforce that rule no matter what the `amount` value is
			for i := 0; i < amount; i++ {
				copy.Spec.CapacityReservations = append(copy.Spec.CapacityReservations, v1alpha1.CapacityReservation{
					EffectiveTime:  metav1.Time{Time: now},
					ExpirationTime: metav1.Time{Time: now.Add(scale.trigger.Duration.Duration)},
					Replicas:       1,
				})
			}
			added += amount
		} else if amount < 0 {
			scale.log.V(2).Info("Removing capacity reservation", "amount", -amount)

			// Remove the requested number of reservations unless there are not that many left
			if len(copy.Spec.CapacityReservations) > -amount {
				copy.Spec.CapacityReservations = copy.Spec.CapacityReservations[-amount:]
			} else {
				copy.Spec.CapacityReservations = nil
			}
			// This "completed" represents the number of completed and therefore removed runners in this batch,
			// which is logged later.
			// As the amount is negative for a scale-down trigger, we make the "completed" amount positive by negating the amount.
			// That way, the user can see the number of removed runners(like 3), rather than the delta (like -3) in the number of runners.
			completed -= amount
		}
	}

	after := len(copy.Spec.CapacityReservations)

	s.Log.V(1).Info(
		fmt.Sprintf("Patching hra %s for capacityReservations update", hra.Name),
		"before", before,
		"expired", expired,
		"added", added,
		"completed", completed,
		"after", after,
	)

	return copy, nil
}
