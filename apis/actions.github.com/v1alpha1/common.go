package v1alpha1

// ResourceMeta carries metadata common to all internal resources
type ResourceMeta struct {
	// +optional
	Labels map[string]string `json:"labels"`
	// +optional
	Annotations map[string]string `json:"annotations"`
}
