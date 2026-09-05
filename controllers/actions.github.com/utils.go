package actionsgithubcom

import "sigs.k8s.io/controller-runtime/pkg/client"

func FilterLabels(labels map[string]string, filter string) map[string]string {
	filtered := map[string]string{}

	for k, v := range labels {
		if k != filter {
			filtered[k] = v
		}
	}

	return filtered
}

type once[T client.Object] struct {
	value T
	fn    func() T
	done  bool
}

func newOnce[T client.Object](fn func() T) *once[T] {
	return &once[T]{
		fn: fn,
	}
}

func (o *once[T]) Do() T {
	if !o.done {
		o.value = o.fn()
		o.done = true
	}
	return o.value
}

func (o *once[T]) Get() T {
	if !o.Called() {
		panic("once.Get called before Do")
	}
	return o.value
}

func (o *once[T]) Called() bool {
	return o.done
}
