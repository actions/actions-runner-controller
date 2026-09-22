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

// ScalerConfig configures the Kubernetes clients used by the ghalistener scaler.
//
// The scaler talks to the API server over two independent clients, because the
// two kinds of traffic have very different shapes and only one of them is on the
// critical path:
//
//   - The scale client publishes the desired runner count. That is one patch per
//     message, and it is the only patch that creates runners, so it must never
//     queue behind anything.
//   - The job client patches job started events. That is two calls per event and
//     can be a whole batch at once, so it is the traffic that actually consumes
//     the rate limit.
//
// A single shared client lets a batch of job event patches drain the token
// bucket ahead of the scale patch, which delays the only call new jobs are
// waiting on. Splitting them keeps the scale patch clear of that backlog.
type ScalerConfig struct {
	// QPS is the query per second limit of the client that patches job started
	// events. This is the bulk of the scaler's API traffic, at up to two calls
	// per job started event.
	// +optional
	// +kubebuilder:validation:Minimum:=1
	QPS *int `json:"qps,omitempty"`

	// Burst is the burst limit of the client that patches job started events.
	// +optional
	// +kubebuilder:validation:Minimum:=1
	Burst *int `json:"burst,omitempty"`

	// ScaleQPS is the query per second limit of the client that publishes the
	// desired runner count. The scaler issues at most one such patch per scale
	// set message, so this only has to be large enough that the patch never
	// waits on a token; it is deliberately a small budget separate from QPS
	// rather than a share of it.
	// +optional
	// +kubebuilder:validation:Minimum:=1
	ScaleQPS *int `json:"scaleQPS,omitempty"`

	// ScaleBurst is the burst limit of the client that publishes the desired
	// runner count.
	// +optional
	// +kubebuilder:validation:Minimum:=1
	ScaleBurst *int `json:"scaleBurst,omitempty"`

	// Workers is the number of job started events the scaler patches
	// concurrently. The events are drained from a background queue rather than
	// being tied to the message they arrived on, so this bounds how many job
	// patches are in flight at any moment, not how many a single message may
	// carry.
	//
	// Raising it past what QPS sustains does nothing, since the rate limiter
	// rather than the worker count is what bounds throughput.
	// +optional
	// +kubebuilder:validation:Minimum:=1
	Workers *int `json:"workers,omitempty"`
}
