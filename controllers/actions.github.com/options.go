package actionsgithubcom

import (
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Options is the optional configuration for the controllers, which can be
// set via command-line flags or environment variables.
type Options struct {
	// AutoscalingRunnerSetMaxConcurrentReconciles is the maximum number of concurrent Reconciles
	// which can be run by the AutoscalingRunnerSetController. Values of zero or less are replaced
	// by the shipped default in Resolve.
	AutoscalingRunnerSetMaxConcurrentReconciles int

	// AutoscalingListenerMaxConcurrentReconciles is the maximum number of concurrent Reconciles
	// which can be run by the AutoscalingListenerController. Values of zero or less are replaced
	// by the shipped default in Resolve.
	AutoscalingListenerMaxConcurrentReconciles int

	// EphemeralRunnerSetMaxConcurrentReconciles is the maximum number of concurrent Reconciles
	// which can be run by the EphemeralRunnerSetController. Values of zero or less are replaced
	// by the shipped default in Resolve.
	EphemeralRunnerSetMaxConcurrentReconciles int

	// EphemeralRunnerMaxConcurrentReconciles is the maximum number of concurrent Reconciles
	// which can be run by the EphemeralRunnerController. Values of zero or less are replaced
	// by the shipped default in Resolve.
	EphemeralRunnerMaxConcurrentReconciles int
}

// OptionsWithDefault returns the default options.
// This is here to maintain the options and their default values in one place,
// rather than having to correlate those in multiple places.
//
// The EphemeralRunner controller gets a higher value than the rest because it
// is the only one of the four that routinely has many distinct objects to
// reconcile at once: one per runner. controller-runtime serialises reconciles
// per object, so extra workers on the other three controllers only pay off
// when many runner scale sets exist.
func OptionsWithDefault() Options {
	return Options{
		AutoscalingRunnerSetMaxConcurrentReconciles: 2,
		AutoscalingListenerMaxConcurrentReconciles:  2,
		EphemeralRunnerSetMaxConcurrentReconciles:   2,
		EphemeralRunnerMaxConcurrentReconciles:      4,
	}
}

// Resolve returns a copy of the options where every MaxConcurrentReconciles of
// zero or less is replaced by that controller's shipped default, so a
// non-positive value can never reach controller-runtime.
func (o Options) Resolve() Options {
	defaults := OptionsWithDefault()
	orDefault := func(n, def int) int {
		if n > 0 {
			return n
		}
		return def
	}
	o.AutoscalingRunnerSetMaxConcurrentReconciles = orDefault(o.AutoscalingRunnerSetMaxConcurrentReconciles, defaults.AutoscalingRunnerSetMaxConcurrentReconciles)
	o.AutoscalingListenerMaxConcurrentReconciles = orDefault(o.AutoscalingListenerMaxConcurrentReconciles, defaults.AutoscalingListenerMaxConcurrentReconciles)
	o.EphemeralRunnerSetMaxConcurrentReconciles = orDefault(o.EphemeralRunnerSetMaxConcurrentReconciles, defaults.EphemeralRunnerSetMaxConcurrentReconciles)
	o.EphemeralRunnerMaxConcurrentReconciles = orDefault(o.EphemeralRunnerMaxConcurrentReconciles, defaults.EphemeralRunnerMaxConcurrentReconciles)
	return o
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

// WithTypedRateLimiter sets the rate limiter for the controller's workqueue.
//
// By default, the controller-runtime uses
// workqueue.DefaultTypedControllerRateLimiter[reconcile.Request], which combines
// an exponential backoff per-item limiter with a token bucket overall limiter
// (10 QPS, 100 bucket size). In large-scale environments with many runner
// scale sets, the token bucket limiter can become a bottleneck for
// reconciliation throughput.
//
// Use this option to override the default rate limiter, for example, to use
// workqueue.DefaultTypedItemBasedRateLimiter[reconcile.Request], which removes
// the overall token bucket constraint while keeping the per-item exponential
// backoff.
func WithTypedRateLimiter(rateLimiter workqueue.TypedRateLimiter[reconcile.Request]) Option {
	return func(b *controller.Options) {
		b.RateLimiter = rateLimiter
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
