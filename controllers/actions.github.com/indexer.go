package actionsgithubcom

import (
	"context"
	"slices"

	v1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetupIndexers(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.Pod{},
		resourceOwnerKey,
		newGroupVersionOwnerKindIndexer("AutoscalingListener", "EphemeralRunner"),
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.ServiceAccount{},
		resourceOwnerKey,
		newGroupVersionOwnerKindIndexer("AutoscalingListener"),
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.EphemeralRunnerSet{},
		resourceOwnerKey,
		newGroupVersionOwnerKindIndexer("AutoscalingRunnerSet"),
	); err != nil {
		return err
	}

	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.EphemeralRunner{},
		resourceOwnerKey,
		newGroupVersionOwnerKindIndexer("EphemeralRunnerSet"),
	); err != nil {
		return err
	}

	return nil
}

func newGroupVersionOwnerKindIndexer(ownerKind string, otherOwnerKinds ...string) client.IndexerFunc {
	owners := append([]string{ownerKind}, otherOwnerKinds...)
	return func(o client.Object) []string {
		groupVersion := v1alpha1.GroupVersion.String()
		owner := metav1.GetControllerOfNoCopy(o)
		if owner == nil {
			return nil
		}

		// ...make sure it is owned by this controller
		if owner.APIVersion != groupVersion || !slices.Contains(owners, owner.Kind) {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}
}

// isControlledBy applies the same ownership test as the resourceOwnerKey index,
// for callers that cannot use that index. It is registered on the manager's
// cache, so a read that deliberately bypasses the cache has to filter here
// instead: the API server rejects .metadata.controller as an unsupported field
// label. Keeping the predicate alongside the indexer is what stops the two
// drifting apart.
func isControlledBy(o client.Object, ownerKind, ownerName string) bool {
	owner := metav1.GetControllerOfNoCopy(o)
	return owner != nil &&
		owner.APIVersion == v1alpha1.GroupVersion.String() &&
		owner.Kind == ownerKind &&
		owner.Name == ownerName
}
