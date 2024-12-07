package actionsgithubcom

import (
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

// Options is the optional configuration for the controllers, which can be
// set via command-line flags or environment variables.
type Options struct {
	// RunnerMaxConcurrentReconciles is the maximum number of concurrent Reconciles which can be run
	// by the EphemeralRunnerController.
	RunnerMaxConcurrentReconciles int
}

// OptionsWithDefault returns the default options.
// This is here to maintain the options and their default values in one place,
// rather than having to correlate those in multiple places.
func OptionsWithDefault() Options {
	return Options{
		RunnerMaxConcurrentReconciles: 2,
	}
}

type Option func(*controller.Options)

// WithMaxConcurrentReconciles sets the maximum number of concurrent Reconciles which can be run.
//
// This is useful to improve the throughput of the controller, but it may also increase the load on the API server and
// the external service (e.g. GitHub API). The default value is 1, as defined by the controller-runtime.
//
// See https://github.com/actions/actions-runner-controller/issues/3021 for more information
// on real-world use cases and the potential impact of this option.
func WithMaxConcurrentReconciles(n int) Option {
	return func(b *controller.Options) {
		b.MaxConcurrentReconciles = n
	}
}

// builderWithOptions applies the given options to the provided builder, if any.
// This is a helper function to avoid the need to import the controller-runtime package in every reconciler source file
// and the command package that creates the controller.
// This is also useful for reducing code duplication around setting controller options in
// multiple reconcilers.
func builderWithOptions(b *builder.Builder, opts []Option) *builder.Builder {
	if len(opts) == 0 {
		return b
	}

	var controllerOpts controller.Options
	for _, opt := range opts {
		opt(&controllerOpts)
	}

	return b.WithOptions(controllerOpts)
}
