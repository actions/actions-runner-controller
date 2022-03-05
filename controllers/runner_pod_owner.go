package controllers

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type podsForOwner struct {
	total        int
	completed    int
	running      int
	terminating  int
	regTimeout   int
	pending      int
	templateHash string
	runner       *v1alpha1.Runner
	statefulSet  *appsv1.StatefulSet
	owner        owner
	synced       bool
	pods         []corev1.Pod
}

type owner interface {
	client.Object

	pods(context.Context, client.Client) ([]corev1.Pod, error)
	templateHash() (string, bool)
	withAnnotation(k, v string) client.Object
	synced() bool
}

type ownerRunner struct {
	client.Object

	Log    logr.Logger
	Runner *v1alpha1.Runner
}

var _ owner = (*ownerRunner)(nil)

func (r *ownerRunner) pods(ctx context.Context, c client.Client) ([]corev1.Pod, error) {
	var pod corev1.Pod

	if err := c.Get(ctx, types.NamespacedName{Namespace: r.Runner.Namespace, Name: r.Runner.Name}, &pod); err != nil {
		r.Log.Error(err, "Failed to get pod managed by runner")
		return nil, err
	}

	return []corev1.Pod{pod}, nil
}

func (r *ownerRunner) templateHash() (string, bool) {
	return getRunnerTemplateHash(r.Runner)
}

func (r *ownerRunner) withAnnotation(k, v string) client.Object {
	copy := r.Runner.DeepCopy()
	setAnnotation(&copy.ObjectMeta, k, v)
	return copy
}

func (r *ownerRunner) synced() bool {
	return true
}

type ownerStatefulSet struct {
	client.Object

	Log         logr.Logger
	StatefulSet *appsv1.StatefulSet
}

var _ owner = (*ownerStatefulSet)(nil)

func (s *ownerStatefulSet) pods(ctx context.Context, c client.Client) ([]corev1.Pod, error) {
	var podList corev1.PodList

	if err := c.List(ctx, &podList, client.MatchingLabels(s.StatefulSet.Spec.Template.ObjectMeta.Labels)); err != nil {
		s.Log.Error(err, "Failed to list pods managed by statefulset")
		return nil, err
	}

	var pods []corev1.Pod

	for _, pod := range podList.Items {
		if owner := metav1.GetControllerOf(&pod); owner == nil || owner.Kind != "StatefulSet" || owner.Name != s.StatefulSet.Name {
			continue
		}

		pods = append(pods, pod)
	}

	return pods, nil
}

func (s *ownerStatefulSet) templateHash() (string, bool) {
	return getStatefulSetTemplateHash(s.StatefulSet)
}

func (s *ownerStatefulSet) withAnnotation(k, v string) client.Object {
	copy := s.StatefulSet.DeepCopy()
	setAnnotation(&copy.ObjectMeta, k, v)
	return copy
}

func (s *ownerStatefulSet) synced() bool {
	var replicas int32 = 1
	if s.StatefulSet.Spec.Replicas != nil {
		replicas = *s.StatefulSet.Spec.Replicas
	}

	if s.StatefulSet.Status.Replicas != replicas {
		s.Log.V(2).Info("Waiting for statefulset to sync", "desiredReplicas", replicas, "currentReplicas", s.StatefulSet.Status.Replicas)
		return false
	}

	return true
}

func getPodsForOwner(ctx context.Context, c client.Client, log logr.Logger, o client.Object) (*podsForOwner, error) {
	var (
		owner       owner
		runner      *v1alpha1.Runner
		statefulSet *appsv1.StatefulSet
	)

	switch v := o.(type) {
	case *v1alpha1.Runner:
		owner = &ownerRunner{
			Object: v,
			Log:    log,
			Runner: v,
		}
		runner = v
	case *appsv1.StatefulSet:
		owner = &ownerStatefulSet{
			Object:      v,
			Log:         log,
			StatefulSet: v,
		}
		statefulSet = v
	default:
		return nil, fmt.Errorf("BUG: Unsupported runner pods owner %v(%T)", v, v)
	}

	pods, err := owner.pods(ctx, c)
	if err != nil {
		return nil, err
	}

	var completed, running, terminating, regTimeout, pending, total int

	for _, pod := range pods {
		total++

		if runnerPodOrContainerIsStopped(&pod) {
			completed++
		} else if pod.Status.Phase == corev1.PodRunning {
			if podRunnerID(&pod) == "" && podConditionTransitionTimeAfter(&pod, corev1.PodReady, registrationTimeout) {
				log.Info(
					"Runner failed to register itself to GitHub in timely manner. "+
						"Recreating the pod to see if it resolves the issue. "+
						"CAUTION: If you see this a lot, you should investigate the root cause. "+
						"See https://github.com/actions-runner-controller/actions-runner-controller/issues/288",
					"creationTimestamp", pod.CreationTimestamp,
					"readyTransitionTime", podConditionTransitionTime(&pod, corev1.PodReady, corev1.ConditionTrue),
					"configuredRegistrationTimeout", registrationTimeout,
				)

				regTimeout++
			} else {
				running++
			}
		} else if !pod.DeletionTimestamp.IsZero() {
			terminating++
		} else {
			// pending includes running but timedout runner's pod too
			pending++
		}
	}

	templateHash, ok := owner.templateHash()
	if !ok {
		log.Info("Failed to get template hash of statefulset. It must be in an invalid state. Please manually delete the statefulset so that it is recreated")

		return nil, nil
	}

	synced := owner.synced()

	return &podsForOwner{
		total:        total,
		completed:    completed,
		running:      running,
		terminating:  terminating,
		regTimeout:   regTimeout,
		pending:      pending,
		templateHash: templateHash,
		runner:       runner,
		statefulSet:  statefulSet,
		owner:        owner,
		synced:       synced,
		pods:         pods,
	}, nil
}

func getRunnerTemplateHash(r *v1alpha1.Runner) (string, bool) {
	hash, ok := r.Labels[LabelKeyRunnerTemplateHash]

	return hash, ok
}

func getStatefulSetTemplateHash(rs *appsv1.StatefulSet) (string, bool) {
	hash, ok := rs.Labels[LabelKeyRunnerTemplateHash]

	return hash, ok
}

type state struct {
	podsForOwners map[string][]*podsForOwner
	lastSyncTime  *time.Time
}

type result struct {
	currentObjects []*podsForOwner
}

func syncRunnerPodsOwners(ctx context.Context, c client.Client, log logr.Logger, effectiveTime *metav1.Time, newDesiredReplicas int, desiredTemplateHash string, create client.Object, ephemeral bool, owners []client.Object) (*result, error) {
	state, err := collectPodsForOwners(ctx, c, log, owners)
	if err != nil || state == nil {
		return nil, err
	}

	podsForOwnersPerTemplateHash, lastSyncTime := state.podsForOwners, state.lastSyncTime

	// # Why do we recreate statefulsets instead of updating their desired replicas?
	//
	// A statefulset cannot add more pods when not all the pods are running.
	// Our ephemeral runners' pods that have finished running become Completed(Phase=Succeeded).
	// So creating one statefulset per a batch of ephemeral runners is the only way for us to add more replicas.
	//
	// # Why do we recreate statefulsets instead of updating fields other than replicas?
	//
	// That's because Kubernetes doesn't allow updating anything other than replicas, template, and updateStrategy.
	// And the nature of ephemeral runner pods requires you to create a statefulset per a batch of new runner pods so
	// we have really no other choice.
	//
	// If you're curious, the below is the error message you will get when you tried to update forbidden StatefulSet field(s):
	//
	// 2021-06-13T07:19:52.760Z        ERROR   actions-runner-controller.runnerset     Failed to patch statefulset
	// {"runnerset": "default/example-runnerset", "error": "StatefulSet.apps \"example-runnerset\" is invalid: s
	// pec: Forbidden: updates to statefulset spec for fields other than 'replicas', 'template', and 'updateStrategy'
	// are forbidden"}
	//
	// Even though the error message includes "Forbidden", this error's reason is "Invalid".
	// So we used to match these errors by using errors.IsInvalid. But that's another story...

	currentObjects := podsForOwnersPerTemplateHash[desiredTemplateHash]

	sort.SliceStable(currentObjects, func(i, j int) bool {
		return currentObjects[i].owner.GetCreationTimestamp().Time.Before(currentObjects[j].owner.GetCreationTimestamp().Time)
	})

	if len(currentObjects) > 0 {
		timestampFirst := currentObjects[0].owner.GetCreationTimestamp()
		timestampLast := currentObjects[len(currentObjects)-1].owner.GetCreationTimestamp()
		var names []string
		for _, ss := range currentObjects {
			names = append(names, ss.owner.GetName())
		}
		log.V(2).Info("Detected some current object(s)", "creationTimestampFirst", timestampFirst, "creationTimestampLast", timestampLast, "names", names)
	}

	var pending, running, regTimeout int

	for _, ss := range currentObjects {
		pending += ss.pending
		running += ss.running
		regTimeout += ss.regTimeout
	}

	log.V(2).Info(
		"Found some pods across owner(s)",
		"pending", pending,
		"running", running,
		"regTimeout", regTimeout,
		"desired", newDesiredReplicas,
		"owners", len(owners),
	)

	maybeRunning := pending + running

	if newDesiredReplicas > maybeRunning && ephemeral && lastSyncTime != nil && effectiveTime != nil && lastSyncTime.After(effectiveTime.Time) {
		log.V(2).Info("Detected that some ephemeral runners have disappeared. Usually this is due to that ephemeral runner completions so ARC does not create new runners until EffectiveTime is updated.", "lastSyncTime", metav1.Time{Time: *lastSyncTime}, "effectiveTime", *effectiveTime, "desired", newDesiredReplicas, "pending", pending, "running", running)
	} else if newDesiredReplicas > maybeRunning {
		num := newDesiredReplicas - maybeRunning

		for i := 0; i < num; i++ {
			// Add more replicas
			if err := c.Create(ctx, create); err != nil {
				return nil, err
			}
		}

		log.V(2).Info("Created object(s) to add more replicas", "num", num)

		return nil, nil
	} else if newDesiredReplicas <= running {
		var retained int

		var delete []*podsForOwner
		for i := len(currentObjects) - 1; i >= 0; i-- {
			ss := currentObjects[i]

			if ss.running == 0 || retained >= newDesiredReplicas {
				// In case the desired replicas is satisfied until i-1, or this owner has no running pods,
				// this owner can be considered safe for deletion.
				// Note that we already waited on this owner to create pods by waiting for
				// `.Status.Replicas`(=total number of pods managed by owner, regardless of the runner is Running or Completed) to match the desired replicas in a previous step.
				// So `.running == 0` means "the owner has created the desired number of pods before, and all of them are completed now".
				delete = append(delete, ss)
			} else if retained < newDesiredReplicas {
				retained += ss.running
			}
		}

		if retained == newDesiredReplicas {
			for _, ss := range delete {
				log := log.WithValues("owner", types.NamespacedName{Namespace: ss.owner.GetNamespace(), Name: ss.owner.GetName()})
				// Statefulset termination process 1/4: Set unregistrationRequestTimestamp only after all the pods managed by the statefulset have
				// started unregistreation process.
				//
				// NOTE: We just mark it instead of immediately starting the deletion process.
				// Otherwise, the runner pod may hit termiationGracePeriod before the unregistration completes(the max terminationGracePeriod is limited to 1h by K8s and a job can be run for more than that),
				// or actions/runner may potentially misbehave on SIGTERM immediately sent by K8s.
				// We'd better unregister first and then start a pod deletion process.
				// The annotation works as a mark to start the pod unregistration and deletion process of ours.
				for _, po := range ss.pods {
					if _, err := annotatePodOnce(ctx, c, log, &po, AnnotationKeyUnregistrationRequestTimestamp, time.Now().Format(time.RFC3339)); err != nil {
						return nil, err
					}
				}

				if _, ok := getAnnotation(ss.owner, AnnotationKeyUnregistrationRequestTimestamp); !ok {
					updated := ss.owner.withAnnotation(AnnotationKeyUnregistrationRequestTimestamp, time.Now().Format(time.RFC3339))

					if err := c.Patch(ctx, updated, client.MergeFrom(ss.owner)); err != nil {
						log.Error(err, fmt.Sprintf("Failed to patch object to have %s annotation", AnnotationKeyUnregistrationRequestTimestamp))
						return nil, err
					}

					log.V(2).Info("Redundant object has been annotated to start the unregistration before deletion")
				} else {
					log.V(2).Info("BUG: Redundant object was already annotated")
				}
			}
			return nil, err
		} else if retained > newDesiredReplicas {
			log.V(2).Info("Waiting sync before scale down", "retained", retained, "newDesiredReplicas", newDesiredReplicas)

			return nil, nil
		} else {
			log.Info("Invalid state", "retained", retained, "newDesiredReplicas", newDesiredReplicas)
			panic("crashed due to invalid state")
		}
	}

	for _, sss := range podsForOwnersPerTemplateHash {
		for _, ss := range sss {
			if ss.templateHash != desiredTemplateHash {
				if ss.owner.GetDeletionTimestamp().IsZero() {
					if err := c.Delete(ctx, ss.owner); err != nil {
						log.Error(err, "Unable to delete object")
						return nil, err
					}

					log.V(2).Info("Deleted redundant and outdated object")
				}

				return nil, nil
			}
		}
	}

	return &result{
		currentObjects: currentObjects,
	}, nil
}

func collectPodsForOwners(ctx context.Context, c client.Client, log logr.Logger, owners []client.Object) (*state, error) {
	podsForOwnerPerTemplateHash := map[string][]*podsForOwner{}

	// lastSyncTime becomes non-nil only when there are one or more owner(s) hence there are same number of runner pods.
	// It's used to prevent runnerset-controller from recreating "completed ephemeral runners".
	// This is needed to prevent runners from being terminated prematurely.
	// See https://github.com/actions-runner-controller/actions-runner-controller/issues/911 for more context.
	//
	// This becomes nil when there are zero statefulset(s). That's fine because then there should be zero stateful(s) to be recreated either hence
	// we don't need to guard with lastSyncTime.
	var lastSyncTime *time.Time

	for _, ss := range owners {
		log := log.WithValues("owner", types.NamespacedName{Namespace: ss.GetNamespace(), Name: ss.GetName()})

		res, err := getPodsForOwner(ctx, c, log, ss)
		if err != nil {
			return nil, err
		}

		// Statefulset termination process 4/4: Let Kubernetes cascade-delete the statefulset and the pods.
		//
		// If the runner is already marked for deletion(=has a non-zero deletion timestamp) by the runner controller (can be caused by an ephemeral runner completion)
		// or by this controller (in case it was deleted in the previous reconcilation loop),
		// we don't need to bother calling GitHub API to re-mark the runner for deletion.
		// Just hold on, and runners will disappear as long as the runner controller is up and running.
		if !res.owner.GetDeletionTimestamp().IsZero() {
			continue
		}

		// Statefulset termination process 3/4: Set the deletionTimestamp to let Kubernetes start a cascade deletion of the statefulset and the pods.
		if _, ok := getAnnotation(res.owner, AnnotationKeyUnregistrationCompleteTimestamp); ok {
			if err := c.Delete(ctx, res.owner); err != nil {
				log.Error(err, "Failed to delete owner")
				return nil, err
			}
			continue
		}

		// Statefulset termination process 2/4: Set unregistrationCompleteTimestamp only if all the pods managed by the statefulset
		// have either unregistered or being deleted.
		if _, ok := getAnnotation(res.owner, AnnotationKeyUnregistrationRequestTimestamp); ok {
			var deletionSafe int
			for _, po := range res.pods {
				if _, ok := getAnnotation(&po, AnnotationKeyUnregistrationCompleteTimestamp); ok {
					deletionSafe++
				} else if !po.DeletionTimestamp.IsZero() {
					deletionSafe++
				}
			}

			log.V(2).Info("Marking owner for unregistration completion", "deletionSafe", deletionSafe, "total", res.total)

			if deletionSafe == res.total {
				if _, ok := getAnnotation(res.owner, AnnotationKeyUnregistrationCompleteTimestamp); !ok {
					updated := res.owner.withAnnotation(AnnotationKeyUnregistrationCompleteTimestamp, time.Now().Format(time.RFC3339))

					if err := c.Patch(ctx, updated, client.MergeFrom(res.owner)); err != nil {
						log.Error(err, fmt.Sprintf("Failed to patch owner to have %s annotation", AnnotationKeyUnregistrationCompleteTimestamp))
						return nil, err
					}

					log.V(2).Info("Redundant owner has been annotated to start the deletion")
				} else {
					log.V(2).Info("BUG: Redundant owner was already annotated to start the deletion")
				}
			}

			continue
		}

		if annotations := res.owner.GetAnnotations(); annotations != nil {
			if a, ok := annotations[SyncTimeAnnotationKey]; ok {
				t, err := time.Parse(time.RFC3339, a)
				if err == nil {
					if lastSyncTime == nil || lastSyncTime.Before(t) {
						lastSyncTime = &t
					}
				}
			}
		}

		// A completed owner and a completed runner pod can safely be deleted without
		// a race condition so delete it here,
		// so that the later process can be a bit simpler.
		if res.total > 0 && res.total == res.completed {
			if err := c.Delete(ctx, ss); err != nil {
				log.Error(err, "Unable to delete owner")
				return nil, err
			}

			log.V(2).Info("Deleted completed owner")

			return nil, nil
		}

		if !res.synced {
			return nil, nil
		}

		podsForOwnerPerTemplateHash[res.templateHash] = append(podsForOwnerPerTemplateHash[res.templateHash], res)
	}

	return &state{podsForOwnerPerTemplateHash, lastSyncTime}, nil
}
