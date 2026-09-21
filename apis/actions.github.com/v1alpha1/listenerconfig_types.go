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
