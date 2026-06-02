package actionsgithubcom

import (
	"context"

	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type kubernetesObject[T kclient.Object] interface {
	kclient.Object
	DeepCopy() T
}

type patcher interface {
	Patch(ctx context.Context, obj kclient.Object, patch kclient.Patch, opts ...kclient.PatchOption) error
}

func patch[T kubernetesObject[T]](ctx context.Context, client patcher, obj T, update func(obj T)) error {
	original := obj.DeepCopy()
	update(obj)
	return client.Patch(ctx, obj, kclient.MergeFrom(original))
}

type subResourcePatcher interface {
	Patch(ctx context.Context, obj kclient.Object, patch kclient.Patch, opts ...kclient.SubResourcePatchOption) error
}

func patchSubResource[T kubernetesObject[T]](ctx context.Context, client subResourcePatcher, obj T, update func(obj T)) error {
	original := obj.DeepCopy()
	update(obj)
	return client.Patch(ctx, obj, kclient.MergeFrom(original))
}
