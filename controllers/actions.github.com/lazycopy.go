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
// Routing a mutation through Mutate is what guarantees that ordering:
//
//	runner := newLazyCopy(&ephemeralRunner)
//	if !controllerutil.ContainsFinalizer(&ephemeralRunner, name) {
//		controllerutil.AddFinalizer(runner.Mutate(), name)
//	}
//	if runner.Modified() {
//		err := r.Patch(ctx, &ephemeralRunner, runner.MergeFrom())
//	}
//
// Callers must uphold that ordering themselves, because lazyCopy cannot
// enforce it. The caller keeps the pointer it passed to newLazyCopy, and as the
// example shows it goes on using that pointer to read the object and to address
// the patch. Nothing stops it from writing through it as well. A write that
// lands before the first Mutate is already present in the snapshot, so the
// merge patch against that snapshot is empty and the write is silently dropped
// rather than sent to the API server.
//
// So the invariant is: read through the original as much as you like, but make
// every mutation that the patch should carry go through Mutate.
//
// "The patch" is the qualifier that matters for a type with a status
// subresource, because there the object is two independently patchable
// surfaces. A write to status cannot go missing from a patch that does not
// carry status: the API server ignores status in the body of a merge patch to
// the main resource, so a status write made outside Mutate is neither captured
// by nor dropped from that patch. EphemeralRunnerSetReconciler.updateStatus
// relies on this, writing Status directly while Reconcile holds a lazyCopy over
// the same object and persisting it through its own Status().Patch. That reads
// like a violation of the rule above and is not one.
//
// The reverse is not true. A lazyCopy guarding a status patch has the same
// exposure to metadata writes, so the rule holds surface by surface rather than
// object by object.
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
// Mutate call, with any of controller-runtime's merge options applied, such as
// client.MergeFromWithOptimisticLock{}. It panics when called on an unmodified
// lazyCopy, because there is no snapshot to diff against and the caller would
// otherwise silently issue a patch computed from the live object against
// itself. Guard it with Modified.
func (l *lazyCopy[T]) MergeFrom(opts ...client.MergeFromOption) client.Patch {
	if !l.copied {
		panic("lazyCopy: MergeFrom called before Mutate")
	}
	return client.MergeFromWithOptions(l.original, opts...)
}
