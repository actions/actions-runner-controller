// Package kjobmgr is a poorly named k8s job manager used to start a Runner with a given JIT config.
package kjobmgr

import (
	"context"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/pkg/errors"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func CreateJob(ctx context.Context, jitConfig *github.RunnerScaleSetJitRunnerConfig) (*batchv1.Job, error) {
	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	job := defaultJobResource(jitConfig.EncodedJITConfig)
	createdJob, err := clientset.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "could not create job")
	}

	return createdJob, nil
}
