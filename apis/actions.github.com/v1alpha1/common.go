package v1alpha1

// ConditionTypeReady is the condition type reported in the status of all resources in this API group.
const ConditionTypeReady = "Ready"

// ResourceMeta carries metadata common to all internal resources
type ResourceMeta struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}
