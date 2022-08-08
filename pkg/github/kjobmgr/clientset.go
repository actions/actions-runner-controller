// Package kjobmgr is a poorly named k8s job manager used to start a Runner with a given JIT config.
package kjobmgr

import (
	"context"
	"fmt"

	"encoding/json"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/pkg/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func CreateJob(ctx context.Context, jitConfig *github.RunnerScaleSetJitRunnerConfig, namespace string) (*v1alpha1.RunnerJob, error) {
	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	job := runnerJobResource(jitConfig.EncodedJITConfig, jitConfig.Runner.Id, namespace) // defaultJobResource(jitConfig.EncodedJITConfig, jitConfig.Runner.Id, namespace)

	body, err := json.Marshal(job)
	if err != nil {
		return nil, errors.Wrap(err, "could not marshal job")
	}

	_, err = clientset.RESTClient().
		Post().
		AbsPath(fmt.Sprintf("/apis/actions.summerwind.dev/v1alpha1/namespaces/%s/runnerjobs", namespace)).
		Body(body).
		DoRaw(context.TODO())
	if err != nil {
		return nil, errors.Wrap(err, "could not create job")
	}

	return job, nil
}
