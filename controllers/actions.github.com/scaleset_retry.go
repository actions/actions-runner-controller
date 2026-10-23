package actionsgithubcom

import (
	"context"
	"errors"
	"time"

	"github.com/actions/scaleset"
	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

func requeueOnRateLimit(ctx context.Context, result ctrl.Result, err error, log logr.Logger) (ctrl.Result, error) {
	if err == nil || ctx.Err() != nil {
		return result, err
	}

	wait := rateLimitDelay(err)
	if wait == 0 {
		return result, err
	}
	log.Error(err, "Actions service request failed; honoring rate-limit cooldown", "retryAfter", wait)
	return ctrl.Result{RequeueAfter: wait}, nil
}

func rateLimitDelay(err error) time.Duration {
	if rateLimit, ok := err.(*scaleset.RateLimitError); ok {
		if rateLimit.RetryAfter > 0 {
			return rateLimit.RetryAfter
		}
		return time.Minute
	}
	if combined, ok := err.(interface{ Unwrap() []error }); ok {
		var wait time.Duration
		for _, cause := range combined.Unwrap() {
			wait = max(wait, rateLimitDelay(cause))
		}
		return wait
	}
	if cause := errors.Unwrap(err); cause != nil {
		return rateLimitDelay(cause)
	}
	return 0
}
