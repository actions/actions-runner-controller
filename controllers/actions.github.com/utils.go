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
	fn    func(T) *T
	done  bool
}

func (o *once[T]) Do(f func() T) T {
	if !o.done {
		o.value = f()
		o.done = true
	}
	return o.value
}

func (o *once[T]) Get() T {
	if !o.done {
		panic("not done")
	}
	return o.value
}
