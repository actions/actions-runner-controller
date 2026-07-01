package actionsgithubcom

import (
	"context"
	"sync"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient"
	"github.com/go-logr/logr"
	"go.uber.org/multierr"
	"golang.org/x/sync/errgroup"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	ephemeralRunnerSetKubernetesWriteConcurrency = 8
	ephemeralRunnerSetActionsServiceConcurrency  = 4
)

func (r *EphemeralRunnerSetReconciler) deleteEphemeralRunners(ctx context.Context, ephemeralRunners []*v1alpha1.EphemeralRunner, log logr.Logger) error {
	return runEphemeralRunnerSetBounded(ctx, len(ephemeralRunners), ephemeralRunnerSetKubernetesWriteConcurrency, func(ctx context.Context, i int) error {
		ephemeralRunner := ephemeralRunners[i]
		log.Info("Deleting ephemeral runner", "name", ephemeralRunner.Name)
		if err := r.Delete(ctx, ephemeralRunner); err != nil && !kerrors.IsNotFound(err) {
			return err
		}
		return nil
	})
}

func (r *EphemeralRunnerSetReconciler) deleteEphemeralRunnersWithActionsClient(ctx context.Context, ephemeralRunners []*v1alpha1.EphemeralRunner, actionsClient multiclient.Client, log logr.Logger) (int, error) {
	var mu sync.Mutex
	deletedCount := 0
	err := runEphemeralRunnerSetBounded(ctx, len(ephemeralRunners), ephemeralRunnerSetActionsServiceConcurrency, func(ctx context.Context, i int) error {
		ephemeralRunner := ephemeralRunners[i]
		log.Info("Removing the ephemeral runner from the service", "name", ephemeralRunner.Name)
		ok, err := r.deleteEphemeralRunnerWithActionsClient(ctx, ephemeralRunner, actionsClient, log)
		if err != nil {
			return err
		}
		if ok {
			mu.Lock()
			deletedCount++
			mu.Unlock()
		}
		return nil
	})
	return deletedCount, err
}

func (r *EphemeralRunnerSetReconciler) deleteUpToEphemeralRunnersWithActionsClient(ctx context.Context, ephemeralRunners []*v1alpha1.EphemeralRunner, count int, actionsClient multiclient.Client, log logr.Logger) error {
	var errs []error
	for start, remaining := 0, count; start < len(ephemeralRunners) && remaining > 0; {
		end := start + remaining
		if end > len(ephemeralRunners) {
			end = len(ephemeralRunners)
		}
		deleted, err := r.deleteEphemeralRunnersWithActionsClient(ctx, ephemeralRunners[start:end], actionsClient, log)
		if err != nil {
			errs = append(errs, err)
		}
		remaining -= deleted
		start = end
	}
	return multierr.Combine(errs...)
}

func runEphemeralRunnerSetBounded(ctx context.Context, count int, concurrency int, fn func(context.Context, int) error) error {
	if count == 0 {
		return nil
	}
	if concurrency <= 0 || concurrency > count {
		concurrency = count
	}

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	var mu sync.Mutex
	var errs []error
	for i := range count {
		g.Go(func() error {
			if err := fn(ctx, i); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	return multierr.Combine(errs...)
}
