package v1alpha1

// ResourceMeta carries metadata common to all internal resources
type ResourceMeta struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}
