package object

import (
	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ActionsGitHubObject interface {
	client.Object
	GitHubConfigUrl() string
	GitHubConfigSecret() string
	GitHubProxy() *v1alpha1.ProxyConfig
	GitHubServerTLS() *v1alpha1.TLSConfig
	VaultConfig() *v1alpha1.VaultConfig
	VaultProxy() *v1alpha1.ProxyConfig
}
