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
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/actions/actions-runner-controller/hash"
	"golang.org/x/net/http/httpproxy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:JSONPath=".spec.minRunners",name=Minimum Runners,type=number
//+kubebuilder:printcolumn:JSONPath=".spec.maxRunners",name=Maximum Runners,type=number
//+kubebuilder:printcolumn:JSONPath=".status.currentRunners",name=Current Runners,type=number
//+kubebuilder:printcolumn:JSONPath=".status.state",name=State,type=string

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
	GitHubServerTLS *GitHubServerTLSConfig `json:"githubServerTLS,omitempty"`

	// Required
	Template corev1.PodTemplateSpec `json:"template,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	MaxRunners *int `json:"maxRunners,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	MinRunners *int `json:"minRunners,omitempty"`
}

type GitHubServerTLSConfig struct {
	// Required
	RootCAsConfigMapRef string `json:"certConfigMapRef,omitempty"`
}

type ProxyConfig struct {
	// +optional
	HTTP *ProxyServerConfig `json:"http,omitempty"`

	// +optional
	HTTPS *ProxyServerConfig `json:"https,omitempty"`

	// +optional
	NoProxy []string `json:"noProxy,omitempty"`
}

func (c *ProxyConfig) toHTTPProxyConfig(secretFetcher func(string) (*corev1.Secret, error)) (*httpproxy.Config, error) {
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
	config, err := c.toHTTPProxyConfig(secretFetcher)
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
	config, err := c.toHTTPProxyConfig(secretFetcher)
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

// AutoscalingRunnerSetStatus defines the observed state of AutoscalingRunnerSet
type AutoscalingRunnerSetStatus struct {
	// +optional
	CurrentRunners int `json:"currentRunners,omitempty"`

	// +optional
	State string `json:"state,omitempty"`
}

func (ars *AutoscalingRunnerSet) ListenerSpecHash() string {
	type listenerSpec = AutoscalingRunnerSetSpec
	arsSpec := ars.Spec.DeepCopy()
	spec := arsSpec
	return hash.ComputeTemplateHash(&spec)
}

func (ars *AutoscalingRunnerSet) RunnerSetSpecHash() string {
	type runnerSetSpec struct {
		GitHubConfigUrl    string
		GitHubConfigSecret string
		RunnerGroup        string
		RunnerScaleSetName string
		Proxy              *ProxyConfig
		GitHubServerTLS    *GitHubServerTLSConfig
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

//+kubebuilder:object:root=true

// AutoscalingRunnerSetList contains a list of AutoscalingRunnerSet
type AutoscalingRunnerSetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutoscalingRunnerSet `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutoscalingRunnerSet{}, &AutoscalingRunnerSetList{})
}
