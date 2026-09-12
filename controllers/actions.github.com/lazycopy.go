package actionsgithubcom

import "sigs.k8s.io/controller-runtime/pkg/client"

// deepCopyObject is satisfied by every generated API type pointer, as well as
// by the built-in Kubernetes types.
type deepCopyObject[T any] interface {
	client.Object
	DeepCopy() T
}

// lazyCopy defers the DeepCopy of a fetched object until the moment it is
// actually about to be mutated.
//
// Reconcilers observe an object far more often than they change it, so taking
// the snapshot up front means paying for a full deep copy on every reconcile
// just to serve the rare patch. lazyCopy pays for it only on the reconciles
// that patch.
//
// The snapshot must be taken before the first mutation, otherwise the merge
// patch is computed against the already mutated object and comes out empty.
// Mutate is the only way to reach the object, which makes that ordering
// impossible to get wrong:
//
//	runner := newLazyCopy(&ephemeralRunner)
//	if !controllerutil.ContainsFinalizer(&ephemeralRunner, name) {
//		controllerutil.AddFinalizer(runner.Mutate(), name)
//	}
//	if runner.Modified() {
//		err := r.Patch(ctx, &ephemeralRunner, runner.MergeFrom())
//	}
//
// A lazyCopy is not safe for concurrent use.
type lazyCopy[T deepCopyObject[T]] struct {
	obj      T
	original T
	copied   bool
}

// newLazyCopy returns a lazyCopy guarding obj. No copy is taken until the
// first call to Mutate.
func newLazyCopy[T deepCopyObject[T]](obj T) *lazyCopy[T] {
	return &lazyCopy[T]{obj: obj}
}

// Mutate snapshots the object on its first call and returns the live object so
// the caller can modify it. Every mutation that a later patch should carry must
// go through Mutate.
func (l *lazyCopy[T]) Mutate() T {
	if !l.copied {
		l.original = l.obj.DeepCopy()
		l.copied = true
	}
	return l.obj
}

// Modified reports whether Mutate has been called, and therefore whether there
// is anything to patch.
func (l *lazyCopy[T]) Modified() bool {
	return l.copied
}

// MergeFrom returns a merge patch against the snapshot taken by the first
// Mutate call. It panics when called on an unmodified lazyCopy, because there
// is no snapshot to diff against and the caller would otherwise silently issue
// a patch computed from the live object against itself. Guard it with
// Modified.
func (l *lazyCopy[T]) MergeFrom() client.Patch {
	if !l.copied {
		panic("lazyCopy: MergeFrom called before Mutate")
	}
	return client.MergeFrom(l.original)
}
