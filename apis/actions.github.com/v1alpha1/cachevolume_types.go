package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// CommitPolicy restricts which finished runners may save their volume as the new
	// cache. Runners that do not match still start from the cache, but their changes
	// are discarded. It protects the cache from being modified by untrusted workflows.
	// All runners may commit when it is not set.
	// +optional
	CommitPolicy *CacheCommitPolicy `json:"commitPolicy,omitempty"`

	// Retention limits how long a cache lives. Without it a cache would only ever grow,
	// because every runner starts from the previous cache and adds to it.
	// +optional
	Retention *CacheRetention `json:"retention,omitempty"`
}

// CacheRetention limits the lifetime of a scale set's cache. A duration of 0 disables
// the limit it belongs to.
type CacheRetention struct {
	// MaxAge is the longest a cache history is continued. Once the history started from an
	// empty volume this long ago, new runners start from an empty volume again and the old
	// snapshot is deleted, which bounds the growth of the cache. Defaults to 336h (14 days).
	// +optional
	MaxAge *metav1.Duration `json:"maxAge,omitempty"`

	// IdleTimeout is how long a snapshot is kept when no runner restores from it and no
	// runner saves a newer one. The snapshot is deleted afterwards, so a scale set that
	// stopped running jobs does not keep its storage forever. Defaults to 168h (7 days).
	// +optional
	IdleTimeout *metav1.Duration `json:"idleTimeout,omitempty"`
}

// CacheCommitPolicy restricts which jobs may advance the cache of a scale set.
//
// Every list that is set has to match: a runner commits only if its job satisfies
// all of them. A list that is not set does not restrict. The policy fails closed:
// when a list is set and the matching property of the job is not known, for example
// because the runner never received a job, the runner does not commit.
//
// Patterns use path.Match syntax, where "*" matches any sequence of characters
// except "/". A pattern that is not valid never matches.
type CacheCommitPolicy struct {
	// Repositories are patterns for the "owner/repo" of the repository that runs the job,
	// for example "my-org/*".
	// +optional
	Repositories []string `json:"repositories,omitempty"`

	// Refs are patterns for the git ref of the workflow, for example "refs/heads/main" or
	// "refs/heads/release/*". It is the ref of the workflow file, as GitHub reports it in the
	// job workflow reference. Prefer Events to keep pull request jobs out: a job triggered by
	// pull_request_target runs the workflow from the base branch ref.
	// +optional
	Refs []string `json:"refs,omitempty"`

	// Events are the names of the events that triggered the workflow and that may commit, for
	// example "push", "schedule" and "workflow_dispatch". Names are compared exactly.
	// +optional
	Events []string `json:"events,omitempty"`
}

// GetVolumeName returns the name of the volume added to the runner pod.
func (c *CacheVolume) GetVolumeName() string {
	if c == nil || c.VolumeName == "" {
		return DefaultCacheVolumeName
	}
	return c.VolumeName
}
