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

package v1alpha1

import (
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/actions/actions-runner-controller/hash"
	"github.com/actions/actions-runner-controller/vault"
	"golang.org/x/net/http/httpproxy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:JSONPath=".spec.minRunners",name=Minimum Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".spec.maxRunners",name=Maximum Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.currentRunners",name=Current Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.state",name=State,type=string
// +kubebuilder:printcolumn:JSONPath=".status.pendingEphemeralRunners",name=Pending Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.runningEphemeralRunners",name=Running Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.finishedEphemeralRunners",name=Finished Runners,type=integer
// +kubebuilder:printcolumn:JSONPath=".status.deletingEphemeralRunners",name=Deleting Runners,type=integer

// AutoscalingRunnerSet is the Schema for the autoscalingrunnersets API
type AutoscalingRunnerSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AutoscalingRunnerSetSpec   `json:"spec,omitempty"`
	Status AutoscalingRunnerSetStatus `json:"status,omitempty"`
}

// AutoscalingRunnerSetSpec defines the desired state of AutoscalingRunnerSet
type AutoscalingRunnerSetSpec struct {
	// Required
	GitHubConfigUrl string `json:"githubConfigUrl,omitempty"`

	// Required
	GitHubConfigSecret string `json:"githubConfigSecret,omitempty"`

	// +optional
	RunnerGroup string `json:"runnerGroup,omitempty"`

	// +optional
	RunnerScaleSetName string `json:"runnerScaleSetName,omitempty"`

	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`

	// +optional
	GitHubServerTLS *TLSConfig `json:"githubServerTLS,omitempty"`

	// +optional
	VaultConfig *VaultConfig `json:"vaultConfig,omitempty"`

	// Required
	Template corev1.PodTemplateSpec `json:"template,omitempty"`

	// +optional
	ListenerMetrics *MetricsConfig `json:"listenerMetrics,omitempty"`

	// +optional
	ListenerTemplate *corev1.PodTemplateSpec `json:"listenerTemplate,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	MaxRunners *int `json:"maxRunners,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	MinRunners *int `json:"minRunners,omitempty"`
}

type TLSConfig struct {
	// Required
	CertificateFrom *TLSCertificateSource `json:"certificateFrom,omitempty"`
}

func (c *TLSConfig) ToCertPool(keyFetcher func(name, key string) ([]byte, error)) (*x509.CertPool, error) {
	if c.CertificateFrom == nil {
		return nil, fmt.Errorf("certificateFrom not specified")
	}

	if c.CertificateFrom.ConfigMapKeyRef == nil {
		return nil, fmt.Errorf("configMapKeyRef not specified")
	}

	cert, err := keyFetcher(c.CertificateFrom.ConfigMapKeyRef.Name, c.CertificateFrom.ConfigMapKeyRef.Key)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to fetch key %q in configmap %q: %w",
			c.CertificateFrom.ConfigMapKeyRef.Key,
			c.CertificateFrom.ConfigMapKeyRef.Name,
			err,
		)
	}

	systemPool, err := x509.SystemCertPool()
	if err != nil {
		return nil, fmt.Errorf("failed to get system cert pool: %w", err)
	}

	pool := systemPool.Clone()
	if !pool.AppendCertsFromPEM(cert) {
		return nil, fmt.Errorf("failed to parse certificate")
	}

	return pool, nil
}

type TLSCertificateSource struct {
	// Required
	ConfigMapKeyRef *corev1.ConfigMapKeySelector `json:"configMapKeyRef,omitempty"`
}

type ProxyConfig struct {
	// +optional
	HTTP *ProxyServerConfig `json:"http,omitempty"`

	// +optional
	HTTPS *ProxyServerConfig `json:"https,omitempty"`

	// +optional
	NoProxy []string `json:"noProxy,omitempty"`
}

func (c *ProxyConfig) ToHTTPProxyConfig(secretFetcher func(string) (*corev1.Secret, error)) (*httpproxy.Config, error) {
	config := &httpproxy.Config{
		NoProxy: strings.Join(c.NoProxy, ","),
	}

	if c.HTTP != nil {
		u, err := url.Parse(c.HTTP.Url)
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy http url %q: %w", c.HTTP.Url, err)
		}

		if c.HTTP.CredentialSecretRef != "" {
			secret, err := secretFetcher(c.HTTP.CredentialSecretRef)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to get secret %s for http proxy: %w",
					c.HTTP.CredentialSecretRef,
					err,
				)
			}

			u.User = url.UserPassword(
				string(secret.Data["username"]),
				string(secret.Data["password"]),
			)
		}

		config.HTTPProxy = u.String()
	}

	if c.HTTPS != nil {
		u, err := url.Parse(c.HTTPS.Url)
		if err != nil {
			return nil, fmt.Errorf("failed to parse proxy https url %q: %w", c.HTTPS.Url, err)
		}

		if c.HTTPS.CredentialSecretRef != "" {
			secret, err := secretFetcher(c.HTTPS.CredentialSecretRef)
			if err != nil {
				return nil, fmt.Errorf(
					"failed to get secret %s for https proxy: %w",
					c.HTTPS.CredentialSecretRef,
					err,
				)
			}

			u.User = url.UserPassword(
				string(secret.Data["username"]),
				string(secret.Data["password"]),
			)
		}

		config.HTTPSProxy = u.String()
	}

	return config, nil
}

func (c *ProxyConfig) ToSecretData(secretFetcher func(string) (*corev1.Secret, error)) (map[string][]byte, error) {
	config, err := c.ToHTTPProxyConfig(secretFetcher)
	if err != nil {
		return nil, err
	}

	data := map[string][]byte{}
	data["http_proxy"] = []byte(config.HTTPProxy)
	data["https_proxy"] = []byte(config.HTTPSProxy)
	data["no_proxy"] = []byte(config.NoProxy)

	return data, nil
}

func (c *ProxyConfig) ProxyFunc(secretFetcher func(string) (*corev1.Secret, error)) (func(*http.Request) (*url.URL, error), error) {
	config, err := c.ToHTTPProxyConfig(secretFetcher)
	if err != nil {
		return nil, err
	}

	proxyFunc := func(req *http.Request) (*url.URL, error) {
		return config.ProxyFunc()(req.URL)
	}

	return proxyFunc, nil
}

type ProxyServerConfig struct {
	// Required
	Url string `json:"url,omitempty"`

	// +optional
	CredentialSecretRef string `json:"credentialSecretRef,omitempty"`
}

type VaultConfig struct {
	// +optional
	Type vault.VaultType `json:"type,omitempty"`
	// +optional
	AzureKeyVault *AzureKeyVaultConfig `json:"azureKeyVault,omitempty"`
	// +optional
	Proxy *ProxyConfig `json:"proxy,omitempty"`
}

type AzureKeyVaultConfig struct {
	// +required
	URL string `json:"url,omitempty"`
	// +required
	TenantID string `json:"tenantId,omitempty"`
	// +required
	ClientID string `json:"clientId,omitempty"`
	// +required
	CertificatePath string `json:"certificatePath,omitempty"`
}

// MetricsConfig holds configuration parameters for each metric type
type MetricsConfig struct {
	// +optional
	Counters map[string]*CounterMetric `json:"counters,omitempty"`
	// +optional
	Gauges map[string]*GaugeMetric `json:"gauges,omitempty"`
	// +optional
	Histograms map[string]*HistogramMetric `json:"histograms,omitempty"`
}

// CounterMetric holds configuration of a single metric of type Counter
type CounterMetric struct {
	Labels []string `json:"labels"`
}

// GaugeMetric holds configuration of a single metric of type Gauge
type GaugeMetric struct {
	Labels []string `json:"labels"`
}

// HistogramMetric holds configuration of a single metric of type Histogram
type HistogramMetric struct {
	Labels  []string  `json:"labels"`
	Buckets []float64 `json:"buckets,omitempty"`
}

// AutoscalingRunnerSetStatus defines the observed state of AutoscalingRunnerSet
type AutoscalingRunnerSetStatus struct {
	// +optional
	CurrentRunners int `json:"currentRunners"`

	// +optional
	State string `json:"state"`

	// EphemeralRunner counts separated by the stage ephemeral runners are in, taken from the EphemeralRunnerSet

	// +optional
	PendingEphemeralRunners int `json:"pendingEphemeralRunners"`
	// +optional
	RunningEphemeralRunners int `json:"runningEphemeralRunners"`
	// +optional
	FailedEphemeralRunners int `json:"failedEphemeralRunners"`
}

func (ars *AutoscalingRunnerSet) ListenerSpecHash() string {
	arsSpec := ars.Spec.DeepCopy()
	spec := arsSpec
	return hash.ComputeTemplateHash(&spec)
}

func (ars *AutoscalingRunnerSet) GitHubConfigSecret() string {
	return ars.Spec.GitHubConfigSecret
}

func (ars *AutoscalingRunnerSet) GitHubConfigUrl() string {
	return ars.Spec.GitHubConfigUrl
}

func (ars *AutoscalingRunnerSet) GitHubProxy() *ProxyConfig {
	return ars.Spec.Proxy
}

func (ars *AutoscalingRunnerSet) GitHubServerTLS() *TLSConfig {
	return ars.Spec.GitHubServerTLS
}

func (ars *AutoscalingRunnerSet) VaultConfig() *VaultConfig {
	return ars.Spec.VaultConfig
}

func (ars *AutoscalingRunnerSet) VaultProxy() *ProxyConfig {
	if ars.Spec.VaultConfig != nil {
		return ars.Spec.VaultConfig.Proxy
	}
	return nil
}

func (ars *AutoscalingRunnerSet) RunnerSetSpecHash() string {
	type runnerSetSpec struct {
		GitHubConfigUrl    string
		GitHubConfigSecret string
		RunnerGroup        string
		RunnerScaleSetName string
		Proxy              *ProxyConfig
		GitHubServerTLS    *TLSConfig
		Template           corev1.PodTemplateSpec
	}
	spec := &runnerSetSpec{
		GitHubConfigUrl:    ars.Spec.GitHubConfigUrl,
		GitHubConfigSecret: ars.Spec.GitHubConfigSecret,
		RunnerGroup:        ars.Spec.RunnerGroup,
		RunnerScaleSetName: ars.Spec.RunnerScaleSetName,
		Proxy:              ars.Spec.Proxy,
		GitHubServerTLS:    ars.Spec.GitHubServerTLS,
		Template:           ars.Spec.Template,
	}
	return hash.ComputeTemplateHash(&spec)
}

// +kubebuilder:object:root=true

// AutoscalingRunnerSetList contains a list of AutoscalingRunnerSet
type AutoscalingRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutoscalingRunnerSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutoscalingRunnerSet{}, &AutoscalingRunnerSetList{})
}
