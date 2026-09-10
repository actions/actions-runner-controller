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
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// EphemeralRunnerSetFinalizerName is the finalizer name used in EphemeralRunnerSet resource to protect the cleanup process of the child ephemeral runners and proxy secret.
	EphemeralRunnerSetFinalizerName = "ephemeralrunnerset.actions.github.com/finalizer"
)

// EphemeralRunnerSetReconciler reconciles a EphemeralRunnerSet object
type EphemeralRunnerSetReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
	// APIReader reads straight from the API server, bypassing the manager's
	// cache. It is needed where the controller has to observe a status field it
	// wrote itself in an earlier reconcile, because the informer cache is not
	// guaranteed to have caught up by the time the next reconcile runs.
	// SetupWithManager fills this in from the manager when it is left unset.
	APIReader client.Reader
	ResourceBuilder
}

// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunnersets/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=actions.github.com,resources=ephemeralrunners/status,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// The responsibility of this controller is to bring the state to the desired one, but it should
// avoid patching itself, because of the frequent patches that the listener is doing.
// The safe point where we can patch the resource is when we are reacting on finalizer.
// Then, the listener should be deleted first, to allow controller clean up resources without interruptions
//
// The resource should be created with finalizer. To leave it to this controller to add it, we would
// risk the same issue of patching the status. Responsibility of this controller should only
// be to bring the count of EphemeralRunners to the desired one, not to patch this resource
// until it is safe to do so
func (r *EphemeralRunnerSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ephemeralrunnerset", req.NamespacedName)

	var ephemeralRunnerSet v1alpha1.EphemeralRunnerSet
	if err := r.Get(ctx, req.NamespacedName, &ephemeralRunnerSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	runnerSet := newLazyCopy(&ephemeralRunnerSet)

	// Requested deletion does not need reconciled.
	if !ephemeralRunnerSet.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
			return ctrl.Result{}, nil
		}

		log.Info("Deleting resources")
		done, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for resources to be deleted")
			return ctrl.Result{}, nil
		}

		done, err = r.cleanUpEphemeralRunnerSetProxySecret(ctx, &ephemeralRunnerSet, log)
		if err != nil {
			log.Error(err, "Failed to clean up EphemeralRunnerSet proxy secret")
			return ctrl.Result{}, err
		}
		if !done {
			log.Info("Waiting for proxy secret to be deleted")
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}

		log.Info("Removing finalizer")
		if controllerutil.RemoveFinalizer(runnerSet.Mutate(), EphemeralRunnerSetFinalizerName) {
			if err := r.Patch(ctx, &ephemeralRunnerSet, runnerSet.MergeFrom()); err != nil {
				log.Error(err, "Failed to update ephemeral runner set with removed finalizer")
				return ctrl.Result{}, err
			}
		}

		log.Info("Successfully removed finalizer after cleanup")
		r.ResourceCache.Delete(&ephemeralRunnerSet)
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&ephemeralRunnerSet, EphemeralRunnerSetFinalizerName) {
		controllerutil.AddFinalizer(runnerSet.Mutate(), EphemeralRunnerSetFinalizerName)
		log.Info("Adding finalizer")
		if err := r.Patch(ctx, &ephemeralRunnerSet, runnerSet.MergeFrom()); err != nil {
			log.Error(err, "Failed to update ephemeral runner set with new finalizer")
			return ctrl.Result{}, err
		}

		log.Info("Successfully added finalizer")
		return ctrl.Result{}, nil
	}

	// If the runner spec revision has advanced past the one that was last
	// successfully applied, delete idle and pending ephemeral runners so they are
	// rebuilt from the new spec.
	if ephemeralRunnerSet.Spec.ActionableRevision > ephemeralRunnerSet.Status.AppliedActionableRevision {
		log.Info(
			"EphemeralRunnerSpec revision has changed, deleting idle or pending ephemeral runners to apply the new spec",
			"specActionableRevision", ephemeralRunnerSet.Spec.ActionableRevision,
			"statusAppliedActionableRevision", ephemeralRunnerSet.Status.AppliedActionableRevision,
		)
		if _, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}

		if _, _, err := r.reconcileEphemeralRunnerSetProxySecret(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to update EphemeralRunnerSet proxy secret")
			return ctrl.Result{}, err
		}

		if err := r.patchAppliedActionableRevisionStatus(ctx, req.NamespacedName, ephemeralRunnerSet.Spec.ActionableRevision); err != nil {
			log.Error(err, "Failed to update EphemeralRunnerSet applied actionable revision status")
			return ctrl.Result{}, err
		}

		log.Info("Updated EphemeralRunnerSet applied actionable revision status", "appliedActionableRevision", ephemeralRunnerSet.Spec.ActionableRevision)
		return ctrl.Result{}, nil
	}

	if ephemeralRunnerSet.Status.Phase == v1alpha1.EphemeralRunnerSetPhaseOutdated {
		if _, err := r.cleanUpEphemeralRunners(ctx, &ephemeralRunnerSet, log); err != nil {
			log.Error(err, "Failed to clean up EphemeralRunners")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Create or update proxy secret if needed
	if _, updated, err := r.reconcileEphemeralRunnerSetProxySecret(ctx, &ephemeralRunnerSet, log); err != nil {
		log.Error(err, "Unable to reconcile ephemeralRunnerSet proxy secret", "namespace", ephemeralRunnerSet.Namespace, "name", proxyEphemeralRunnerSetSecretName(&ephemeralRunnerSet))
		return ctrl.Result{}, err
	} else if updated {
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// Find all EphemeralRunner with matching namespace and own by this EphemeralRunnerSet.
	var ephemeralRunnerList v1alpha1.EphemeralRunnerList
	if err := r.List(
		ctx,
		&ephemeralRunnerList,
		client.InNamespace(req.Namespace),
		client.MatchingFields{resourceOwnerKey: req.Name},
	); err != nil {
		log.Error(err, "Unable to list child ephemeral runners")
		return ctrl.Result{}, err
	}

	ephemeralRunnersByState := newEphemeralRunnersByStates(&ephemeralRunnerList, ephemeralRunnerSet.Status.AppliedActionableRevision)

	log.Info(
		"Ephemeral runner counts",
		"outdated", len(ephemeralRunnersByState.outdated),
		"staleOutdated", len(ephemeralRunnersByState.staleOutdated),
		"pending", len(ephemeralRunnersByState.pending),
		"running", len(ephemeralRunnersByState.running),
		"finished", len(ephemeralRunnersByState.finished),
		"failed", len(ephemeralRunnersByState.failed),
		"deleting", len(ephemeralRunnersByState.deleting),
	)

	// Runners that reported Outdated against a runner spec that has since been
	// replaced are not evidence about the current spec. Drop them so the scaling
	// logic below replaces them with runners built from the current spec, instead
	// of letting them hold the set in the Outdated phase forever.
	if len(ephemeralRunnersByState.staleOutdated) > 0 {
		log.Info(
			"Deleting outdated ephemeral runners created before the last spec update so they can be replaced",
			"count", len(ephemeralRunnersByState.staleOutdated),
			"appliedActionableRevision", ephemeralRunnerSet.Status.AppliedActionableRevision,
		)
		if err := r.deleteTerminatedEphemeralRunners(ctx, ephemeralRunnersByState.staleOutdated, log); err != nil {
			log.Error(err, "failed to delete stale outdated ephemeral runners")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.updateStatus(ctx, &ephemeralRunnerSet, ephemeralRunnersByState, log)
	}

	total := ephemeralRunnersByState.scaleTotal()
	if ephemeralRunnerSet.Spec.PatchID == 0 || ephemeralRunnerSet.Spec.PatchID != ephemeralRunnersByState.latestPatchID {
		// Spec.Replicas is the count the listener asked for when it published
		// Spec.PatchID. Deleting finished runners here changes the live count that
		// the count was computed against, so satisfying it in the same pass would
		// create runners to replace jobs that have already completed. Record the
		// patch ID the cleanup belongs to and return, leaving the scaling decision
		// to the next reconcile, which sees the post-cleanup state.
		if len(ephemeralRunnersByState.finished) > 0 {
			if err := r.patchFinishedRunnerCleanupPatchIDStatus(ctx, req.NamespacedName, ephemeralRunnerSet.Spec.PatchID); err != nil {
				log.Error(err, "failed to update finished runner cleanup patch ID status")
				return ctrl.Result{}, err
			}
			if err := r.deleteTerminatedEphemeralRunners(ctx, ephemeralRunnersByState.finished, log); err != nil {
				log.Error(err, "failed to delete terminated ephemeral runners")
				return ctrl.Result{}, err
			}
			ephemeralRunnerSet.Status.FinishedRunnerCleanupPatchID = ephemeralRunnerSet.Spec.PatchID

			log.Info("Finished ephemeral runners were cleaned up, deferring scaling decision")
			return ctrl.Result{}, r.updateStatus(ctx, &ephemeralRunnerSet, ephemeralRunnersByState, log)
		}

		// Runners that are being deleted still exist and still hold their
		// registration, so counting only the live ones would let the controller
		// create replacements for runners that have not gone away yet.
		scaleUpTotal := total + len(ephemeralRunnersByState.deleting)
		log.Info("Scaling comparison", "current", total, "deleting", len(ephemeralRunnersByState.deleting), "desired", ephemeralRunnerSet.Spec.Replicas)
		switch {
		case scaleUpTotal < ephemeralRunnerSet.Spec.Replicas: // Handle scale up
			// The gap below Spec.Replicas is the one the cleanup above opened for
			// this patch ID, not new demand. Wait for the listener to publish a
			// fresh desired state before acting on it.
			suppressed, err := r.scaleUpServicedByFinishedRunnerCleanup(ctx, req.NamespacedName, &ephemeralRunnerSet)
			if err != nil {
				log.Error(err, "failed to determine whether scale up was already serviced by finished runner cleanup")
				return ctrl.Result{}, err
			}
			if suppressed {
				ephemeralRunnerSet.Status.FinishedRunnerCleanupPatchID = ephemeralRunnerSet.Spec.PatchID
				log.Info("Skipping scale up until listener publishes a fresh desired state after finished runner cleanup", "patchID", ephemeralRunnerSet.Spec.PatchID)
				return ctrl.Result{}, r.updateStatus(ctx, &ephemeralRunnerSet, ephemeralRunnersByState, log)
			}

			count := ephemeralRunnerSet.Spec.Replicas - scaleUpTotal
			log.Info("Creating new ephemeral runners (scale up)", "count", count)
			if err := r.createEphemeralRunners(ctx, &ephemeralRunnerSet, count, log); err != nil {
				log.Error(err, "failed to make ephemeral runner")
				return ctrl.Result{}, err
			}

		case ephemeralRunnerSet.Spec.PatchID > 0 && total >= ephemeralRunnerSet.Spec.Replicas: // Handle scale down scenario.
			// If ephemeral runner did not yet update the phase to succeeded, but the scale down
			// request is issued, we should ignore the scale down request.
			// Eventually, the ephemeral runner will be cleaned up on the next patch request, which happens
			// on the next batch
		case ephemeralRunnerSet.Spec.PatchID == 0 && total > ephemeralRunnerSet.Spec.Replicas:
			count := total - ephemeralRunnerSet.Spec.Replicas
			log.Info("Deleting ephemeral runners (scale down)", "count", count)
			if err := r.deleteIdleEphemeralRunners(
				ctx,
				&ephemeralRunnerSet,
				ephemeralRunnersByState.pending,
				ephemeralRunnersByState.running,
				count,
				log,
			); err != nil {
				log.Error(err, "failed to delete idle runners")
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, r.updateStatus(ctx, &ephemeralRunnerSet, ephemeralRunnersByState, log)
}

// patchAppliedActionableRevisionStatus brings status into line with the runner
// spec carried by targetAppliedRevision once that spec has been fully applied.
// It records the applied revision, clears the scale-up suppression marker when
// the revision actually advances, and re-derives Status.Phase from the child
// runners.
//
// The marker lives in status rather than in an annotation on the spec, and it is
// written only once the cleanup above has actually succeeded. If the controller
// dies part-way through deleting the idle and pending runners, the applied
// revision is still behind the spec revision when it comes back, so the work is
// redone rather than skipped. Writing the marker first, or writing it together
// with the spec, would let a crash leave runners alive that are running a spec
// nobody will ever revisit.
//
// The object is re-fetched inside the retry rather than reusing the copy the
// reconciler already has, because the cleanup can take long enough for that copy
// to go stale, and a conflicting write must not be resolved by replaying an old
// status.
//
// The read bypasses the cache because this also clears
// FinishedRunnerCleanupPatchID, and the patch is computed as a diff against the
// object that was read. A cached read that still showed the field as 0 while the
// API server held a recorded marker would produce a patch with no entry for the
// field, silently leaving the stale marker in place.
//
// The patch carries an optimistic lock so that the re-fetch actually means
// something. A plain merge patch has no resourceVersion precondition, so the API
// server can never reject it as conflicting: RetryOnConflict would never fire,
// and a patch computed from a stale read could move the applied revision
// backwards, re-satisfying the spec > applied comparison above and deleting the
// idle runners all over again. With the lock, the server accepts the write only
// if the re-fetched object is still the live one, so a successful patch proves
// the monotonicity check above was evaluated against live data. A stale attempt
// conflicts and is retried or requeued instead of regressing the marker.
//
// The lock covers the EphemeralRunnerSet object and nothing else. The phase is
// derived from a separate list of the child runners, which no precondition on
// this patch can vouch for, so that list is read through the same authoritative
// reader rather than the cache.
func (r *EphemeralRunnerSetReconciler) patchAppliedActionableRevisionStatus(ctx context.Context, key types.NamespacedName, targetAppliedRevision int64) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var latest v1alpha1.EphemeralRunnerSet
		reader := r.APIReader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, key, &latest); err != nil {
			return err
		}

		// Build the desired status as a value so no copy of the object is taken
		// on the common path where the status already matches.
		desiredStatus := latest.Status

		// Only an advance means the idle and pending runners were just deleted and
		// the listener restarted. Guarding both writes on it keeps this callable
		// as a plain "make sure status reflects revision N" without disturbing a
		// marker that still describes the live patch sequence.
		if latest.Status.AppliedActionableRevision < targetAppliedRevision {
			desiredStatus.AppliedActionableRevision = targetAppliedRevision

			// The marker records a patch ID from the sequence that was current
			// before this spec change. Applying a new revision deletes the idle and
			// pending runners, so the shortfall that follows belongs to the new spec
			// and must be filled. Worse, a spec change restarts the listener, and a
			// restarted listener numbers its patches from 0 upwards, counting
			// through every integer. It therefore passes through a leftover marker
			// value with near-certainty, and would suppress the very scale up that
			// rebuilds the pool.
			desiredStatus.FinishedRunnerCleanupPatchID = 0
		}

		ephemeralRunnerList := new(v1alpha1.EphemeralRunnerList)
		// Listed through the same authoritative reader as the Get above. The
		// optimistic lock on the patch below covers the EphemeralRunnerSet object
		// only, so it cannot vouch for a separately-read list: deriving the phase
		// from the cache would let a successful, lock-protected write carry a
		// value the lock says nothing about. The list also sits inside
		// RetryOnConflict, and a cached list can return the same stale data on
		// every attempt, spending the whole backoff re-deriving one wrong phase.
		//
		// resourceOwnerKey cannot be used here. It is a client-side index
		// registered on the manager's cache, and the API server rejects it as an
		// unsupported field label, so the ownership filter has to be applied in
		// this process instead.
		//
		// Narrowing server-side by label is not a safe alternative either. Label
		// propagation is operator-configurable through
		// --exclude-label-propagation-prefix, so the scale set labels are not
		// guaranteed to reach the runners, and a selector that silently matched
		// none of them would derive the phase from an empty list rather than
		// fail. A namespace can hold more than one scale set, so this does read
		// runners that are not ours, but it only runs when a revision actually
		// advances rather than on every reconcile.
		if err := reader.List(ctx, ephemeralRunnerList, client.InNamespace(latest.Namespace)); err != nil {
			return fmt.Errorf("failed to list child ephemeral runners: %w", err)
		}
		ephemeralRunnerList.Items = slices.DeleteFunc(ephemeralRunnerList.Items, func(runner v1alpha1.EphemeralRunner) bool {
			return !isControlledBy(&runner, "EphemeralRunnerSet", latest.Name)
		})

		// Judge the runners against the revision the set has now applied, rather
		// than the one this call was asked to apply: every runner created before
		// that revision is stale by definition, so its Outdated report says
		// nothing about the current spec. This is what lets a spec update clear
		// the Outdated phase immediately rather than waiting for the pre-update
		// runners to be collected.
		//
		// After the guard above, this field is max(live, target), and the two
		// differ in a case that matters. The caller reads the spec from the
		// cache while this function re-reads the status from the API server, so
		// a lagging reconcile can arrive with a target behind the live marker.
		// Judging against that lower target would rate a runner left over from
		// the superseded revision as current and flip a set that has already
		// moved on back to Outdated. That phase is deliberately absorbing, so
		// the set would then stay switched off until the next spec change.
		state := newEphemeralRunnersByStates(ephemeralRunnerList, latest.Status.AppliedActionableRevision)

		// Set the phase in both directions. This function returns early from
		// Reconcile without reaching updateStatus, so leaving the phase untouched
		// would let a stale value survive: a stale Running would hide genuinely
		// outdated runners from the cleanup path, and a stale Outdated would keep
		// the set switched off after the spec that caused it was replaced.
		if len(state.outdated) > 0 {
			desiredStatus.Phase = v1alpha1.EphemeralRunnerSetPhaseOutdated
		} else {
			desiredStatus.Phase = v1alpha1.EphemeralRunnerSetPhaseRunning
		}

		// Checked after every field above has been set, so that clearing the
		// marker alone is still enough to issue the patch.
		if latest.Status == desiredStatus {
			return nil
		}

		original := latest.DeepCopy()
		latest.Status = desiredStatus

		return r.Status().Patch(ctx, &latest, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	})
}

// scaleUpServicedByFinishedRunnerCleanup reports whether the shortfall against
// Spec.Replicas was created by this controller cleaning up finished runners for
// the patch ID currently in the spec, rather than by new demand from the
// listener.
//
// The marker is written by an earlier reconcile and then read back here, so the
// cached copy handed to Reconcile cannot be trusted: deleting the finished
// runners triggers watch events that schedule the next reconcile, and that
// reconcile can be served from an informer cache that has not yet observed the
// controller's own status write. The decision is therefore always made against
// an uncached read. That confines the extra API call to scale-up decisions,
// where the controller is about to issue creates anyway.
//
// An earlier version short-circuited on a cached hit, on the reasoning that the
// marker was only ever set and so a hit could never be a false positive. That
// reasoning no longer holds: applying a new actionable revision clears the
// marker, so a lagging cache can show a recorded marker that the API server has
// already cleared, and trusting it would suppress exactly the scale up that
// rebuilds the pool after a spec change.
//
// One window remains. A listener that restarts without a spec change keeps the
// marker but starts its patch sequence again from 0 and counts up through every
// integer, so it passes through the recorded value with near-certainty rather
// than by coincidence. If that collision lands on a reconcile that needs to
// scale up, that reconcile is suppressed.
//
// That is a hiccup rather than an outage. The listener calls back into scaling
// on every long-poll timeout, not only when something changes, and once the set
// is idle at its minimum with no job completed it publishes the collapsed patch
// ID 0, which is never suppressed. So the shortfall is filled on the next
// long-poll cycle.
func (r *EphemeralRunnerSetReconciler) scaleUpServicedByFinishedRunnerCleanup(ctx context.Context, key types.NamespacedName, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet) (bool, error) {
	if ephemeralRunnerSet.Spec.PatchID == 0 {
		return false, nil
	}

	if r.APIReader == nil {
		return false, errors.New("APIReader is not configured, cannot confirm the finished runner cleanup patch ID without reading through the cache")
	}

	var latest v1alpha1.EphemeralRunnerSet
	if err := r.APIReader.Get(ctx, key, &latest); err != nil {
		return false, fmt.Errorf("failed to read EphemeralRunnerSet without the cache: %w", err)
	}

	return latest.Status.FinishedRunnerCleanupPatchID == ephemeralRunnerSet.Spec.PatchID, nil
}

// patchFinishedRunnerCleanupPatchIDStatus records that finished runners were
// deleted while serving patchID, so a later reconcile can tell the resulting gap
// below Spec.Replicas apart from genuine new demand.
//
// Like the applied revision above, this is written after the deletions succeed
// and re-fetches the object inside the retry, so a conflicting write is never
// resolved by replaying a status that predates the cleanup.
//
// The patch carries an optimistic lock for the same reason, and the exposure
// here is if anything worse: the check below is an equality test rather than a
// monotonicity test, so this helper is willing to move the marker to whatever
// patch ID the reconcile is carrying, including backwards. Without a
// resourceVersion precondition the API server cannot reject the write, so
// RetryOnConflict can never fire and a reconcile serving an older patch ID can
// overwrite a marker recorded for a newer one. The guard would then stop
// suppressing for the patch ID that was actually serviced, and the controller
// would create the replacement runners this layer exists to prevent.
//
// Re-fetching through the API reader narrows that window to the gap between the
// read and the patch rather than closing it, because the decision is only as
// fresh as the moment it was taken. The lock is what makes the write conditional
// on that decision still holding.
//
// The check below is deliberately an equality test and must not be relaxed into
// the >= monotonicity test the applied revision uses. Applied revisions derive
// from metadata.generation and only ever climb, but listener patch IDs do not:
// setDesiredWorkerState publishes 0 whenever the set is idle at MinRunners with
// nothing dirty, restarts its sequence from 0 when the listener restarts, and
// wraps explicitly at math.MaxInt32. So Spec.PatchID legitimately moves
// backwards, and the marker has to follow it. Refusing to record a lower patch
// ID would strand the marker above every value the listener goes on to publish,
// and since the scale-up guard suppresses only on an exact match, suppression
// would never fire again -- disabling the behaviour this layer exists to add.
//
// That is also why the lock is the right fix rather than a stricter comparison.
// It cannot make an older patch ID unwritable, because the retry re-reads and
// re-applies the same argument; recording the patch ID whose cleanup actually
// happened is a true statement regardless of ordering, and the next cleanup
// re-records. What the lock prevents is a write decided against state that has
// since changed.
func (r *EphemeralRunnerSetReconciler) patchFinishedRunnerCleanupPatchIDStatus(ctx context.Context, key types.NamespacedName, patchID int) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var latest v1alpha1.EphemeralRunnerSet
		reader := r.APIReader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, key, &latest); err != nil {
			return err
		}

		if latest.Status.FinishedRunnerCleanupPatchID == patchID {
			return nil
		}

		original := latest.DeepCopy()
		latest.Status.FinishedRunnerCleanupPatchID = patchID

		return r.Status().Patch(ctx, &latest, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *EphemeralRunnerSetReconciler) updateStatus(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, state *ephemeralRunnersByState, log logr.Logger) error {
	var phase v1alpha1.EphemeralRunnerSetPhase
	switch {
	case len(state.outdated) > 0:
		phase = v1alpha1.EphemeralRunnerSetPhaseOutdated
	case ephemeralRunnerSet.Status.Phase == "":
		phase = v1alpha1.EphemeralRunnerSetPhaseRunning
	default:
		phase = ephemeralRunnerSet.Status.Phase
	}
	desiredStatus := v1alpha1.EphemeralRunnerSetStatus{
		Phase:                        phase,
		AppliedActionableRevision:    ephemeralRunnerSet.Status.AppliedActionableRevision,
		FinishedRunnerCleanupPatchID: ephemeralRunnerSet.Status.FinishedRunnerCleanupPatchID,
	}

	// Update the status if needed.
	if ephemeralRunnerSet.Status != desiredStatus {
		original := ephemeralRunnerSet.DeepCopy()
		ephemeralRunnerSet.Status = desiredStatus
		if err := r.Status().Patch(ctx, ephemeralRunnerSet, client.MergeFrom(original)); err != nil {
			log.Error(err, "Failed to update EphemeralRunnerSet status")
			return err
		}
		log.Info("Updated EphemeralRunnerSet status", "status", ephemeralRunnerSet.Status)

	}
	return nil
}

// deleteTerminatedEphemeralRunners deletes runners that have reached a terminal
// state and are no longer useful, so that the scaling logic can replace them.
func (r *EphemeralRunnerSetReconciler) deleteTerminatedEphemeralRunners(ctx context.Context, ephemeralRunners []*v1alpha1.EphemeralRunner, log logr.Logger) error {
	var errs []error
	for i := range ephemeralRunners {
		log.Info("Deleting terminated ephemeral runner", "name", ephemeralRunners[i].Name, "phase", ephemeralRunners[i].Status.Phase)
		if err := r.Delete(ctx, ephemeralRunners[i]); err != nil {
			if !kerrors.IsNotFound(err) {
				errs = append(errs, err)
			}
		}
	}

	return multierr.Combine(errs...)
}

func (r *EphemeralRunnerSetReconciler) cleanUpProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) error {
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy == nil {
		return nil
	}
	log.Info("Deleting proxy secret")

	proxySecret := new(corev1.Secret)
	proxySecret.Namespace = ephemeralRunnerSet.Namespace
	proxySecret.Name = proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet)

	if err := r.Delete(ctx, proxySecret); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete proxy secret: %w", err)
	}

	log.Info("Deleted proxy secret")

	return nil
}

func (r *EphemeralRunnerSetReconciler) cleanUpEphemeralRunners(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (bool, error) {
	ephemeralRunnerList := new(v1alpha1.EphemeralRunnerList)
	err := r.List(ctx, ephemeralRunnerList, client.InNamespace(ephemeralRunnerSet.Namespace), client.MatchingFields{resourceOwnerKey: ephemeralRunnerSet.Name})
	if err != nil {
		return false, fmt.Errorf("failed to list child ephemeral runners: %w", err)
	}

	// only if there are no ephemeral runners left, return true
	if len(ephemeralRunnerList.Items) == 0 {
		err := r.cleanUpProxySecret(ctx, ephemeralRunnerSet, log)
		if err != nil {
			return false, err
		}
		log.Info("All ephemeral runners are deleted")
		return true, nil
	}

	ephemeralRunnerState := newEphemeralRunnersByStates(ephemeralRunnerList, ephemeralRunnerSet.Status.AppliedActionableRevision)

	log.Info(
		"Clean up runner counts",
		"pending", len(ephemeralRunnerState.pending),
		"running", len(ephemeralRunnerState.running),
		"finished", len(ephemeralRunnerState.finished),
		"failed", len(ephemeralRunnerState.failed),
		"deleting", len(ephemeralRunnerState.deleting),
		"outdated", len(ephemeralRunnerState.outdated),
	)

	log.Info("Cleanup terminated ephemeral runners")
	var errs []error
	for _, ephemeralRunner := range ephemeralRunnerState.terminated() {
		log.Info("Deleting ephemeral runner", "name", ephemeralRunner.Name)
		if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		mergedErrs := multierr.Combine(errs...)
		log.Error(mergedErrs, "Failed to delete ephemeral runners")
		return false, mergedErrs
	}

	// avoid fetching the client if we have nothing left to do
	if len(ephemeralRunnerState.running) == 0 && len(ephemeralRunnerState.pending) == 0 {
		return false, nil
	}

	actionsClient, err := r.GetActionsService(ctx, ephemeralRunnerSet)
	if err != nil {
		return false, err
	}

	log.Info("Cleanup pending or running ephemeral runners")
	errs = errs[0:0]
	for _, ephemeralRunner := range ephemeralRunnerState.pending {
		log.Info("Removing the ephemeral runner from the service", "name", ephemeralRunner.Name)
		_, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
	}

	for _, ephemeralRunner := range ephemeralRunnerState.running {
		if ephemeralRunner.HasJob() {
			log.Info(
				"Skipping ephemeral runner since it is running a job",
				"name", ephemeralRunner.Name,
				"workflowRunId", ephemeralRunner.Status.WorkflowRunID,
				"jobId", ephemeralRunner.Status.JobID,
			)
			continue
		}

		log.Info("Removing the idle ephemeral runner from the service", "name", ephemeralRunner.Name)
		_, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		mergedErrs := multierr.Combine(errs...)
		log.Error(mergedErrs, "Failed to remove ephemeral runners from the service")
		return false, mergedErrs
	}

	return false, nil
}

func (r *EphemeralRunnerSetReconciler) cleanUpEphemeralRunnerSetProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (done bool, err error) {
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy == nil {
		return true, nil
	}
	var proxySecret corev1.Secret
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: ephemeralRunnerSet.Namespace,
			Name:      proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
		},
		&proxySecret,
	)
	switch {
	case err == nil:
		log.Info("Deleting proxy secret")
		if err := r.Delete(ctx, &proxySecret); err != nil && !kerrors.IsNotFound(err) {
			log.Error(err, "Failed to delete proxy secret")
			return false, err
		}
		log.Info("Deleted proxy secret")
		return false, nil
	case kerrors.IsNotFound(err):
		log.Info("Proxy secret already deleted")
		return true, nil
	default:
		log.Error(
			err,
			"Unable to get ephemeralRunnerSet proxy secret",
			"namespace",
			ephemeralRunnerSet.Namespace,
			"name",
			proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
		)
		return false, err
	}
}

func (r *EphemeralRunnerSetReconciler) reconcileEphemeralRunnerSetProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) (secret *corev1.Secret, updated bool, err error) {
	if ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy == nil {
		return nil, false, nil
	}

	var proxySecret corev1.Secret
	err = r.Get(
		ctx,
		types.NamespacedName{
			Namespace: ephemeralRunnerSet.Namespace,
			Name:      proxyEphemeralRunnerSetSecretName(ephemeralRunnerSet),
		},
		&proxySecret,
	)
	switch {
	case err == nil:
		proxySecretData, err := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
			secret := new(corev1.Secret)
			err := r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: s}, secret)
			return secret, err
		})
		if err != nil {
			return nil, false, fmt.Errorf("failed to convert proxy config to secret data: %w", err)
		}

		desiredRunnerSetProxy, err := r.newEphemeralRunnerSetProxySecret(ephemeralRunnerSet, proxySecretData)
		if err != nil {
			return nil, false, fmt.Errorf("failed to build desired ephemeralRunnerSet proxy secret: %w", err)
		}

		dataModified := !maps.EqualFunc(proxySecret.Data, desiredRunnerSetProxy.Data, bytes.Equal)
		desiredLabels := r.filterAndMergeLabels(proxySecret.Labels, desiredRunnerSetProxy.Labels)
		labelsModified := !maps.Equal(proxySecret.Labels, desiredLabels)
		desiredAnnotations := r.mergeAnnotations(proxySecret.Annotations, desiredRunnerSetProxy.Annotations)
		annotationsModified := !maps.Equal(proxySecret.Annotations, desiredAnnotations)
		if dataModified || labelsModified || annotationsModified {
			updatedProxySecret := proxySecret.DeepCopy()
			if dataModified {
				updatedProxySecret.Data = desiredRunnerSetProxy.Data
			}
			if labelsModified {
				updatedProxySecret.Labels = desiredLabels
			}
			if annotationsModified {
				updatedProxySecret.Annotations = desiredAnnotations
			}
			log.Info("Updating ephemeralRunnerSet proxy secret")
			if err := r.Patch(ctx, updatedProxySecret, client.MergeFrom(&proxySecret)); err != nil {
				return nil, false, fmt.Errorf("failed to update ephemeralRunnerSet proxy secret: %w", err)
			}
			return updatedProxySecret, true, nil
		}
		return &proxySecret, false, nil
	case kerrors.IsNotFound(err):
		// Create a compiled secret for the runner pods in the runnerset namespace
		log.Info("Creating a ephemeralRunnerSet proxy secret for the runner pods")
		if err := r.createProxySecret(ctx, ephemeralRunnerSet, log); err != nil {
			return nil, false, fmt.Errorf("failed to create ephemeralRunnerSet proxy secret: %w", err)
		}
		return nil, false, nil
	default:
		return nil, false, err
	}
}

// createEphemeralRunners provisions `count` number of v1alpha1.EphemeralRunner resources in the cluster.
func (r *EphemeralRunnerSetReconciler) createEphemeralRunners(ctx context.Context, runnerSet *v1alpha1.EphemeralRunnerSet, count int, log logr.Logger) error {
	// Track multiple errors at once and return the bundle.
	errs := make([]error, 0)
	for i := range count {
		ephemeralRunner, err := r.newEphemeralRunner(runnerSet)
		if err != nil {
			log.Error(err, "failed to build ephemeral runner")
			errs = append(errs, err)
			continue
		}
		if runnerSet.Spec.EphemeralRunnerSpec.Proxy != nil {
			ephemeralRunner.Spec.ProxySecretRef = proxyEphemeralRunnerSetSecretName(runnerSet)
		}

		log.Info("Creating new ephemeral runner", "progress", i+1, "total", count)
		if err := r.Create(ctx, ephemeralRunner); err != nil {
			log.Error(err, "failed to make ephemeral runner")
			errs = append(errs, err)
			continue
		}

		log.Info("Created new ephemeral runner", "runner", ephemeralRunner.Name)
	}

	return multierr.Combine(errs...)
}

func (r *EphemeralRunnerSetReconciler) createProxySecret(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, log logr.Logger) error {
	proxySecretData, err := ephemeralRunnerSet.Spec.EphemeralRunnerSpec.Proxy.ToSecretData(func(s string) (*corev1.Secret, error) {
		secret := new(corev1.Secret)
		err := r.Get(ctx, types.NamespacedName{Namespace: ephemeralRunnerSet.Namespace, Name: s}, secret)
		return secret, err
	})
	if err != nil {
		return fmt.Errorf("failed to convert proxy config to secret data: %w", err)
	}

	runnerPodProxySecret, err := r.newEphemeralRunnerSetProxySecret(ephemeralRunnerSet, proxySecretData)
	if err != nil {
		log.Error(err, "failed to build proxy secret")
		return err
	}

	log.Info("Creating new proxy secret")
	if err := r.Create(ctx, runnerPodProxySecret); err != nil {
		log.Error(err, "failed to create proxy secret")
		return err
	}

	log.Info("Created new proxy secret")
	return nil
}

// deleteIdleEphemeralRunners try to deletes `count` number of v1alpha1.EphemeralRunner resources in the cluster.
// It will only delete `v1alpha1.EphemeralRunner` that has registered with Actions service
// which has a `v1alpha1.EphemeralRunner.Status.RunnerId` set.
// So, it is possible that this function will not delete enough ephemeral runners
// if there are not enough ephemeral runners that have registered with Actions service.
// When this happens, the next reconcile loop will try to delete the remaining ephemeral runners
// after we get notified by any of the `v1alpha1.EphemeralRunner.Status` updates.
func (r *EphemeralRunnerSetReconciler) deleteIdleEphemeralRunners(ctx context.Context, ephemeralRunnerSet *v1alpha1.EphemeralRunnerSet, pendingEphemeralRunners, runningEphemeralRunners []*v1alpha1.EphemeralRunner, count int, log logr.Logger) error {
	if count <= 0 {
		return nil
	}
	runners := newEphemeralRunnerStepper(pendingEphemeralRunners, runningEphemeralRunners)
	if runners.len() == 0 {
		log.Info("No pending or running ephemeral runners running at this time for scale down")
		return nil
	}
	actionsClient, err := r.GetActionsService(ctx, ephemeralRunnerSet)
	if err != nil {
		return fmt.Errorf("failed to create actions client for ephemeral runner replica set: %w", err)
	}
	var errs []error
	deletedCount := 0
	for runners.next() {
		ephemeralRunner := runners.object()
		isDone := ephemeralRunner.IsDone()
		if !isDone && ephemeralRunner.Status.RunnerID == 0 {
			log.Info("Skipping ephemeral runner since it is not registered yet", "name", ephemeralRunner.Name)
			continue
		}

		if !isDone && ephemeralRunner.HasJob() {
			log.Info(
				"Skipping ephemeral runner since it is running a job",
				"name", ephemeralRunner.Name,
				"workflowRunId", ephemeralRunner.Status.WorkflowRunID,
				"jobId", ephemeralRunner.Status.JobID,
			)
			continue
		}

		log.Info("Removing the idle ephemeral runner", "name", ephemeralRunner.Name)
		ok, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
		if !ok {
			continue
		}

		deletedCount++
		if deletedCount == count {
			break
		}
	}

	return multierr.Combine(errs...)
}

func (r *EphemeralRunnerSetReconciler) deleteEphemeralRunnerWithActionsClient(ctx context.Context, ephemeralRunner *v1alpha1.EphemeralRunner, actionsClient multiclient.Client, log logr.Logger) (bool, error) {
	if err := actionsClient.RemoveRunner(ctx, int64(ephemeralRunner.Status.RunnerID)); err != nil {
		if errors.Is(err, scaleset.JobStillRunningError) {
			log.Info("Runner is still running a job, skipping deletion", "name", ephemeralRunner.Name, "runnerId", ephemeralRunner.Status.RunnerID)
			return false, nil
		}

		return false, err
	}

	log.Info("Deleting ephemeral runner after removing from the service", "name", ephemeralRunner.Name, "runnerId", ephemeralRunner.Status.RunnerID)
	if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
		return false, err
	}

	log.Info("Deleted ephemeral runner", "name", ephemeralRunner.Name, "runnerId", ephemeralRunner.Status.RunnerID)
	return true, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *EphemeralRunnerSetReconciler) SetupWithManager(mgr ctrl.Manager, opts ...Option) error {
	r.setSchemeIfUnset(r.Scheme)

	if r.APIReader == nil {
		r.APIReader = mgr.GetAPIReader()
	}

	return builderWithOptions(
		ctrl.NewControllerManagedBy(mgr).
			For(&v1alpha1.EphemeralRunnerSet{}).
			Owns(&v1alpha1.EphemeralRunner{}, builder.WithPredicates(ephemeralRunnerSetOwnedEphemeralRunnerPredicate())).
			WithEventFilter(predicate.ResourceVersionChangedPredicate{}),
		opts,
	).Complete(r)
}

type ephemeralRunnerStepper struct {
	items []*v1alpha1.EphemeralRunner
	index int
}

func newEphemeralRunnerStepper(primary []*v1alpha1.EphemeralRunner, othersOrdered ...[]*v1alpha1.EphemeralRunner) *ephemeralRunnerStepper {
	sort.Slice(primary, func(i, j int) bool {
		return primary[i].GetCreationTimestamp().Time.Before(primary[j].GetCreationTimestamp().Time)
	})
	for _, bucket := range othersOrdered {
		sort.Slice(bucket, func(i, j int) bool {
			return bucket[i].GetCreationTimestamp().Time.Before(bucket[j].GetCreationTimestamp().Time)
		})
	}

	for _, bucket := range othersOrdered {
		primary = append(primary, bucket...)
	}

	return &ephemeralRunnerStepper{
		items: primary,
		index: -1,
	}
}

func (s *ephemeralRunnerStepper) next() bool {
	if s.index+1 < len(s.items) {
		s.index++
		return true
	}
	return false
}

func (s *ephemeralRunnerStepper) object() *v1alpha1.EphemeralRunner {
	if s.index >= 0 && s.index < len(s.items) {
		return s.items[s.index]
	}
	return nil
}

func (s *ephemeralRunnerStepper) len() int {
	return len(s.items)
}

type ephemeralRunnersByState struct {
	pending  []*v1alpha1.EphemeralRunner
	running  []*v1alpha1.EphemeralRunner
	finished []*v1alpha1.EphemeralRunner
	failed   []*v1alpha1.EphemeralRunner
	deleting []*v1alpha1.EphemeralRunner
	// outdated holds runners that reported Outdated against the runner spec that
	// is currently applied. They are evidence that the current spec is still
	// rejected by the service, so they drive the set into the Outdated phase.
	outdated []*v1alpha1.EphemeralRunner
	// staleOutdated holds runners that reported Outdated against a runner spec
	// that has since been replaced. They say nothing about the current spec, so
	// they must not drive the set into the Outdated phase; they are deleted and
	// replaced by runners built from the current spec instead.
	staleOutdated []*v1alpha1.EphemeralRunner

	latestPatchID int
}

// newEphemeralRunnersByStates groups the child runners by state.
//
// appliedActionableRevision is the EphemeralRunnerSet revision the runners are
// being judged against. A runner that reported Outdated before that revision was
// applied is classified as stale rather than outdated, so that updating the
// runner spec clears the Outdated phase immediately instead of waiting for the
// pre-update runners to disappear.
func newEphemeralRunnersByStates(ephemeralRunnerList *v1alpha1.EphemeralRunnerList, appliedActionableRevision int64) *ephemeralRunnersByState {
	var ephemeralRunnerState ephemeralRunnersByState

	for i := range ephemeralRunnerList.Items {
		r := &ephemeralRunnerList.Items[i]
		patchID, err := strconv.Atoi(r.Annotations[AnnotationKeyPatchID])
		if err == nil && patchID > ephemeralRunnerState.latestPatchID {
			ephemeralRunnerState.latestPatchID = patchID
		}
		if !r.DeletionTimestamp.IsZero() {
			ephemeralRunnerState.deleting = append(ephemeralRunnerState.deleting, r)
			continue
		}

		switch r.Status.Phase {
		case v1alpha1.EphemeralRunnerPhaseRunning:
			ephemeralRunnerState.running = append(ephemeralRunnerState.running, r)
		case v1alpha1.EphemeralRunnerPhaseSucceeded:
			ephemeralRunnerState.finished = append(ephemeralRunnerState.finished, r)
		case v1alpha1.EphemeralRunnerPhaseFailed:
			ephemeralRunnerState.failed = append(ephemeralRunnerState.failed, r)
		case v1alpha1.EphemeralRunnerPhaseOutdated:
			if ephemeralRunnerActionableRevision(r) < appliedActionableRevision {
				ephemeralRunnerState.staleOutdated = append(ephemeralRunnerState.staleOutdated, r)
			} else {
				ephemeralRunnerState.outdated = append(ephemeralRunnerState.outdated, r)
			}
		default:
			// Pending or no phase should be considered as pending.
			//
			// If field is not set, that means that the EphemeralRunner
			// did not yet have chance to update the Status.Phase field.
			ephemeralRunnerState.pending = append(ephemeralRunnerState.pending, r)
		}
	}
	return &ephemeralRunnerState
}

// ephemeralRunnerActionableRevision reports the EphemeralRunnerSet revision the
// runner was created from. Runners created before this annotation existed report
// 0, which matches the zero value of Status.AppliedActionableRevision, so they
// are treated as current until the spec is updated for the first time.
func ephemeralRunnerActionableRevision(ephemeralRunner *v1alpha1.EphemeralRunner) int64 {
	revision, err := strconv.ParseInt(ephemeralRunner.Annotations[AnnotationKeyActionableRevision], 10, 64)
	if err != nil {
		return 0
	}
	return revision
}

func (s *ephemeralRunnersByState) terminated() []*v1alpha1.EphemeralRunner {
	terminated := make([]*v1alpha1.EphemeralRunner, 0, len(s.finished)+len(s.failed)+len(s.outdated)+len(s.staleOutdated))
	terminated = append(terminated, s.finished...)
	terminated = append(terminated, s.failed...)
	terminated = append(terminated, s.outdated...)
	terminated = append(terminated, s.staleOutdated...)
	return terminated
}

func (s *ephemeralRunnersByState) scaleTotal() int {
	return len(s.pending) + len(s.running) + len(s.failed)
}
