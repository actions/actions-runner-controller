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
	"golang.org/x/net/http/httpproxy"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:JSONPath=".spec.minRunners",name=Minimum Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".spec.maxRunners",name=Maximum Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.currentRunners",name=Current Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.state",name=State,type=string
//+kubebuilder:printcolumn:JSONPath=".status.pendingEphemeralRunners",name=Pending Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.runningEphemeralRunners",name=Running Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.finishedEphemeralRunners",name=Finished Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.deletingEphemeralRunners",name=Deleting Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.desiredMinRunners",name=Desired Minimum Runners,type=integer
//+kubebuilder:printcolumn:JSONPath=".status.scheduledOverridesSummary",name=Schedule,type=string

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
	ListenerTemplate *corev1.PodTemplateSpec `json:"listenerTemplate,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	MaxRunners *int `json:"maxRunners,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	MinRunners *int `json:"minRunners,omitempty"`

	// +optional
	// ScheduledOverrides is the list of ScheduledOverride.
	// It can be used to override a few fields of AutoscalingRunnerSetSpec on schedule.
	// The earlier a scheduled override is, the higher it is prioritized.
	// +optional
	ScheduledOverrides []ScheduledOverride `json:"scheduledOverrides,omitempty"`
}

type GitHubServerTLSConfig struct {
	// Required
	CertificateFrom *TLSCertificateSource `json:"certificateFrom,omitempty"`
}

func (c *GitHubServerTLSConfig) ToCertPool(keyFetcher func(name, key string) ([]byte, error)) (*x509.CertPool, error) {
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

// ScheduledOverride can be used to override a few fields of AutoscalingRunnerSetSpec on schedule.
// A schedule can optionally be recurring, so that the corresponding override happens every day, week, month, or year.
type ScheduledOverride struct {
	// StartTime is the time at which the first override starts.
	StartTime metav1.Time `json:"startTime"`

	// EndTime is the time at which the first override ends.
	EndTime metav1.Time `json:"endTime"`

	// MinRunners is the number of runners while overriding.
	// If omitted, it doesn't override minRunners.
	// +optional
	// +nullable
	// +kubebuilder:validation:Minimum=0
	MinRunners *int `json:"minRunners,omitempty"`

	// +optional
	RecurrenceRule RecurrenceRule `json:"recurrenceRule,omitempty"`
}

type RecurrenceRule struct {
	// Frequency is the name of a predefined interval of each recurrence.
	// The valid values are "Daily", "Weekly", "Monthly", and "Yearly".
	// If empty, the corresponding override happens only once.
	// +optional
	// +kubebuilder:validation:Enum=Daily;Weekly;Monthly;Yearly
	Frequency string `json:"frequency,omitempty"`

	// UntilTime is the time of the final recurrence.
	// If empty, the schedule recurs forever.
	// +optional
	UntilTime metav1.Time `json:"untilTime,omitempty"`
}

// AutoscalingRunnerSetStatus defines the observed state of AutoscalingRunnerSet
type AutoscalingRunnerSetStatus struct {
	// +optional
	CurrentRunners int `json:"currentRunners"`

	// +optional
	State string `json:"state"`

	// EphemeralRunner counts separated by the stage ephemeral runners are in, taken from the EphemeralRunnerSet

	//+optional
	PendingEphemeralRunners int `json:"pendingEphemeralRunners"`
	// +optional
	RunningEphemeralRunners int `json:"runningEphemeralRunners"`
	// +optional
	FailedEphemeralRunners int `json:"failedEphemeralRunners"`

	// +optional
	// +kubebuilder:validation:Minimum:=0
	DesiredMinRunners int `json:"desiredMinRunners"`
	// +optional
	ScheduledOverridesSummary *string `json:"scheduledOverridesSummary,omitempty"`
}

func (ars *AutoscalingRunnerSet) ListenerSpecHash() string {
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
