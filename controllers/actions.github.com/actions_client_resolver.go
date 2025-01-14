package actionsgithubcom

import (
	"context"
	"fmt"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/vault"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ActionsClientGetter interface {
	GetActionsClientForAutoscalingRunnerSet(ctx context.Context, ars *v1alpha1.AutoscalingRunnerSet) (actions.ActionsService, error)
	GetActionsClientForEphemeralRunnerSet(ctx context.Context, ers *v1alpha1.EphemeralRunnerSet) (actions.ActionsService, error)
	GetActionsClientForEphemeralRunner(ctx context.Context, er *v1alpha1.EphemeralRunner) (actions.ActionsService, error)
}

var (
	_ ActionsClientGetter = (*ActionsClientSecretResolver)(nil)
	_ ActionsClientGetter = (*ActionsClientVaultResolver)(nil)
)

type ActionsClientSecretResolver struct {
	client.Client
	actions.MultiClient
}

func (r *ActionsClientSecretResolver) GetActionsClientForAutoscalingRunnerSet(ctx context.Context, ars *v1alpha1.AutoscalingRunnerSet) (actions.ActionsService, error) {
	var configSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: ars.Namespace, Name: ars.Spec.GitHubConfigSecret}, &configSecret); err != nil {
		return nil, fmt.Errorf("failed to find GitHub config secret: %w", err)
	}

	opts, err := r.actionsClientOptionsForAutoscalingRunnerSet(ctx, ars)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions client options: %w", err)
	}

	return r.MultiClient.GetClientFromSecret(
		ctx,
		ars.Spec.GitHubConfigUrl,
		ars.Namespace,
		configSecret.Data,
		opts...,
	)
}

func (r *ActionsClientSecretResolver) actionsClientOptionsForAutoscalingRunnerSet(ctx context.Context, autoscalingRunnerSet *v1alpha1.AutoscalingRunnerSet) ([]actions.ClientOption, error) {
	var options []actions.ClientOption

	if autoscalingRunnerSet.Spec.Proxy != nil {
		proxyFunc, err := autoscalingRunnerSet.Spec.Proxy.ProxyFunc(func(s string) (*corev1.Secret, error) {
			var secret corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: autoscalingRunnerSet.Namespace, Name: s}, &secret)
			if err != nil {
				return nil, fmt.Errorf("failed to get proxy secret %s: %w", s, err)
			}

			return &secret, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get proxy func: %w", err)
		}

		options = append(options, actions.WithProxy(proxyFunc))
	}

	tlsConfig := autoscalingRunnerSet.Spec.GitHubServerTLS
	if tlsConfig != nil {
		pool, err := tlsConfig.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := r.Get(
				ctx,
				types.NamespacedName{
					Namespace: autoscalingRunnerSet.Namespace,
					Name:      name,
				},
				&configmap,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get configmap %s: %w", name, err)
			}

			return []byte(configmap.Data[key]), nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tls config: %w", err)
		}

		options = append(options, actions.WithRootCAs(pool))
	}

	return options, nil
}

func (r *ActionsClientSecretResolver) GetActionsClientForEphemeralRunnerSet(ctx context.Context, rs *v1alpha1.EphemeralRunnerSet) (actions.ActionsService, error) {
	secret := new(corev1.Secret)
	if err := r.Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: rs.Spec.EphemeralRunnerSpec.GitHubConfigSecret}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	opts, err := r.actionsClientOptionsForEphemeralRunnerSet(ctx, rs)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions client options: %w", err)
	}

	return r.MultiClient.GetClientFromSecret(
		ctx,
		rs.Spec.EphemeralRunnerSpec.GitHubConfigUrl,
		rs.Namespace,
		secret.Data,
		opts...,
	)
}

func (r *ActionsClientSecretResolver) actionsClientOptionsForEphemeralRunnerSet(ctx context.Context, rs *v1alpha1.EphemeralRunnerSet) ([]actions.ClientOption, error) {
	var opts []actions.ClientOption
	if rs.Spec.EphemeralRunnerSpec.Proxy != nil {
		proxyFunc, err := rs.Spec.EphemeralRunnerSpec.Proxy.ProxyFunc(func(s string) (*corev1.Secret, error) {
			var secret corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: s}, &secret)
			if err != nil {
				return nil, fmt.Errorf("failed to get secret %s: %w", s, err)
			}

			return &secret, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get proxy func: %w", err)
		}

		opts = append(opts, actions.WithProxy(proxyFunc))
	}

	tlsConfig := rs.Spec.EphemeralRunnerSpec.GitHubServerTLS
	if tlsConfig != nil {
		pool, err := tlsConfig.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := r.Get(
				ctx,
				types.NamespacedName{
					Namespace: rs.Namespace,
					Name:      name,
				},
				&configmap,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get configmap %s: %w", name, err)
			}

			return []byte(configmap.Data[key]), nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tls config: %w", err)
		}

		opts = append(opts, actions.WithRootCAs(pool))
	}

	return opts, nil
}

func (r *ActionsClientSecretResolver) GetActionsClientForEphemeralRunner(ctx context.Context, runner *v1alpha1.EphemeralRunner) (actions.ActionsService, error) {
	secret := new(corev1.Secret)
	if err := r.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: runner.Spec.GitHubConfigSecret}, secret); err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	opts, err := r.actionsClientOptionsForEphemeralRunner(ctx, runner)
	if err != nil {
		return nil, fmt.Errorf("failed to get actions client options: %w", err)
	}

	return r.MultiClient.GetClientFromSecret(
		ctx,
		runner.Spec.GitHubConfigUrl,
		runner.Namespace,
		secret.Data,
		opts...,
	)
}

func (r *ActionsClientSecretResolver) actionsClientOptionsForEphemeralRunner(ctx context.Context, runner *v1alpha1.EphemeralRunner) ([]actions.ClientOption, error) {
	var opts []actions.ClientOption
	if runner.Spec.Proxy != nil {
		proxyFunc, err := runner.Spec.Proxy.ProxyFunc(func(s string) (*corev1.Secret, error) {
			var secret corev1.Secret
			err := r.Get(ctx, types.NamespacedName{Namespace: runner.Namespace, Name: s}, &secret)
			if err != nil {
				return nil, fmt.Errorf("failed to get proxy secret %s: %w", s, err)
			}

			return &secret, nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get proxy func: %w", err)
		}

		opts = append(opts, actions.WithProxy(proxyFunc))
	}

	tlsConfig := runner.Spec.GitHubServerTLS
	if tlsConfig != nil {
		pool, err := tlsConfig.ToCertPool(func(name, key string) ([]byte, error) {
			var configmap corev1.ConfigMap
			err := r.Get(
				ctx,
				types.NamespacedName{
					Namespace: runner.Namespace,
					Name:      name,
				},
				&configmap,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to get configmap %s: %w", name, err)
			}

			return []byte(configmap.Data[key]), nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get tls config: %w", err)
		}

		opts = append(opts, actions.WithRootCAs(pool))
	}

	return opts, nil
}

type ActionsClientVaultResolver struct {
	vault.Vault
	actions.MultiClient
}

func (r *ActionsClientVaultResolver) GetActionsClientForAutoscalingRunnerSet(ctx context.Context, ars *v1alpha1.AutoscalingRunnerSet) (actions.ActionsService, error) {
	panic("todo")
}

func (r *ActionsClientVaultResolver) GetActionsClientForEphemeralRunnerSet(ctx context.Context, ers *v1alpha1.EphemeralRunnerSet) (actions.ActionsService, error) {
	panic("todo")
}

func (r *ActionsClientVaultResolver) GetActionsClientForEphemeralRunner(ctx context.Context, er *v1alpha1.EphemeralRunner) (actions.ActionsService, error) {
	panic("todo")
}
