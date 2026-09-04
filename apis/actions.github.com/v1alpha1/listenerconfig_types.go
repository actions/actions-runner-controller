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

// ListenerConfig holds configuration for the ghalistener pod.
type ListenerConfig struct {
	// +optional
	Scaler *ScalerConfig `json:"scaler,omitempty"`
}

// GetScaler returns the ScalerConfig, or nil if not set.
func (c *ListenerConfig) GetScaler() *ScalerConfig {
	if c == nil {
		return nil
	}
	return c.Scaler
}

// ScalerConfig configures the Kubernetes client used by the ghalistener scaler.
type ScalerConfig struct {
	// +optional
	// +kubebuilder:validation:Minimum:=1
	QPS *int `json:"qps,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum:=1
	Burst *int `json:"burst,omitempty"`
}
