// Package runnermanager is a poorly named k8s job manager used to start a Runner with a given JIT config.
package runnermanager

import (
	"context"
	"fmt"

	"encoding/json"

	"github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func CreateJob(ctx context.Context, jitConfig *github.RunnerScaleSetJitRunnerConfig, namespace string) (*v1alpha1.RunnerJob, error) {
	// // Run this app locally (not in cluster) by using a local k8s config to connect to the cluster
	// var kubeconfig = filepath.Join(homedir.HomeDir(), ".kube", "config")
	// conf, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	// if err != nil {
	// 	panic(err.Error())
	// }

	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	runnerJobTemplate := runnerJobResource(jitConfig.EncodedJITConfig, jitConfig.Runner.Id, namespace)

	body, err := json.Marshal(runnerJobTemplate)
	if err != nil {
		return nil, errors.Wrap(err, "could not marshal job")
	}

	runnerJob := &v1alpha1.RunnerJob{}
	err = clientset.RESTClient().
		Post().
		AbsPath(fmt.Sprintf("/apis/actions.summerwind.dev/v1alpha1/namespaces/%s/runnerjobs", namespace)).
		Body(body).
		Do(ctx).
		Into(runnerJob)
	if err != nil {
		return nil, errors.Wrap(err, "could not create job")
	}
	return runnerJob, nil
}

func PatchRunnerDeployment(ctx context.Context, namespace, runnerDeploymentName string, desiredReplicas *int) (*v1alpha1.RunnerDeployment, error) {
	conf, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(conf)
	if err != nil {
		return nil, err
	}

	patchRunnerDeployment := &v1alpha1.RunnerDeployment{
		Spec: v1alpha1.RunnerDeploymentSpec{
			Replicas: desiredReplicas,
		},
	}

	body, err := json.Marshal(patchRunnerDeployment)
	if err != nil {
		return nil, errors.Wrap(err, "could not marshal job")
	}

	patchedRunnerDeployment := &v1alpha1.RunnerDeployment{}

	err = kubeClient.RESTClient().
		Patch(types.MergePatchType).
		Namespace(namespace).
		Resource("RunnerDeployments").
		Body(body).
		Do(ctx).
		Into(patchedRunnerDeployment)
	if err != nil {
		return nil, errors.Wrap(err, "could not create job")
	}

	return patchedRunnerDeployment, nil
}
