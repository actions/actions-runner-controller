package actionsgithubcom

import (
	"context"
	"fmt"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	kclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type object[T kclient.Object] interface {
	kclient.Object
	DeepCopy() T
}

type patcher interface {
	Patch(ctx context.Context, obj kclient.Object, patch kclient.Patch, opts ...kclient.PatchOption) error
}

func patch[T object[T]](ctx context.Context, client patcher, obj T, update func(obj T)) error {
	original := obj.DeepCopy()
	update(obj)
	return client.Patch(ctx, obj, kclient.MergeFrom(original))
}

type subResourcePatcher interface {
	Patch(ctx context.Context, obj kclient.Object, patch kclient.Patch, opts ...kclient.SubResourcePatchOption) error
}

func patchSubResource[T object[T]](ctx context.Context, client subResourcePatcher, obj T, update func(obj T)) error {
	original := obj.DeepCopy()
	update(obj)
	return client.Patch(ctx, obj, kclient.MergeFrom(original))
}

func getProxyUserInfoBySecretNamespacedName(ctx context.Context, client kclient.Reader, namespacedName types.NamespacedName) (*url.Userinfo, error) {
	secret := new(corev1.Secret)
	if err := client.Get(ctx, namespacedName, secret); err != nil {
		return nil, fmt.Errorf("failed to get http proxy credential secret: %w", err)
	}
	return url.UserPassword(string(secret.Data["username"]), string(secret.Data["password"])), nil
}
