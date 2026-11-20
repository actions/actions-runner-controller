package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
)

// DefaultCacheVolumeName is the name of the volume injected into the runner
// pod when CacheVolume.VolumeName is not set.
const DefaultCacheVolumeName = "cache"

// CacheVolume configures a persistent cache for the runners of a scale set.
//
// Every runner gets its own writable PersistentVolumeClaim, restored from the
// most recent snapshot of the scale set's cache. When a runner finishes
// cleanly, its volume is snapshotted and becomes the new most recent snapshot.
// Only the newest snapshot is kept.
//
// The volume is only added to the runner pod. Mount it where it is needed by
// adding volumeMounts to the containers in the pod template.
//
// It requires a StorageClass that supports ReadWriteOnce volumes and a CSI
// driver that supports volume snapshots.
type CacheVolume struct {
	// VolumeName is the name of the volume added to the runner pod.
	// Defaults to "cache".
	// +optional
	// +kubebuilder:validation:MaxLength=63
	VolumeName string `json:"volumeName,omitempty"`

	// StorageClassName is the StorageClass used to provision cache volumes.
	// +required
	// +kubebuilder:validation:MinLength=1
	StorageClassName string `json:"storageClassName"`

	// Size is the requested size of each cache volume. It must be at least the
	// size of the volume the snapshots were taken from.
	// +required
	Size resource.Quantity `json:"size"`

	// SnapshotClassName is the VolumeSnapshotClass used to snapshot cache
	// volumes. The cluster default class is used when it is not set.
	// +optional
	SnapshotClassName *string `json:"snapshotClassName,omitempty"`
}

// GetVolumeName returns the name of the volume added to the runner pod.
func (c *CacheVolume) GetVolumeName() string {
	if c == nil || c.VolumeName == "" {
		return DefaultCacheVolumeName
	}
	return c.VolumeName
}
