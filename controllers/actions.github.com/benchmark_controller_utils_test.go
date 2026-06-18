/*
Copyright 2020 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actionsgithubcom

import (
	"context"
	"testing"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/scaleset"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	benchmarkGitHubConfigURL    = "https://github.com/actions/actions-runner-controller"
	benchmarkGitHubConfigSecret = "test-secret"
	benchmarkRunnerImage        = "ghcr.io/actions/runner:latest"
)

func NewBenchmarkScheme(b *testing.B) *runtime.Scheme {
	b.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		b.Fatalf("add client-go scheme: %v", err)
	}
	if err := actionsv1alpha1.AddToScheme(scheme); err != nil {
		b.Fatalf("add actions.github.com scheme: %v", err)
	}

	return scheme
}

func NewBenchmarkRequest(namespace, name string) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
	}
}

func ResetBenchmarkAutoscalingRunnerSet(b *testing.B, ctx context.Context, k8sClient client.Client, ars *actionsv1alpha1.AutoscalingRunnerSet) {
	b.Helper()

	current := &actionsv1alpha1.AutoscalingRunnerSet{}
	key := client.ObjectKeyFromObject(ars)
	if err := k8sClient.Get(ctx, key, current); err != nil {
		b.Fatalf("get autoscaling runner set %s: %v", key, err)
	}

	ars.ResourceVersion = current.ResourceVersion
	if err := k8sClient.Update(ctx, ars); err != nil {
		b.Fatalf("reset autoscaling runner set %s: %v", key, err)
	}
}

func WarmupIteration(b *testing.B, fn func()) {
	b.Helper()
	b.StopTimer()
	fn()
	b.StartTimer()
}

func RunBenchmarkIterations(b *testing.B, fn func()) {
	b.Helper()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fn()
	}
}

func NewFakeMultiClient() *scalefake.MultiClient {
	runnerGroup := &scaleset.RunnerGroup{ID: 1, Name: "default"}
	runnerScaleSet := &scaleset.RunnerScaleSet{
		ID:              1,
		Name:            "test-scale-set",
		RunnerGroupID:   runnerGroup.ID,
		RunnerGroupName: runnerGroup.Name,
	}
	runner := &scaleset.RunnerReference{ID: 1, Name: "test-runner"}

	return scalefake.NewMultiClient(
		scalefake.WithClient(
			scalefake.NewClient(
				scalefake.WithGetRunnerGroupByName(runnerGroup, nil),
				scalefake.WithGetRunnerScaleSet(runnerScaleSet, nil),
				scalefake.WithGetRunnerScaleSetByID(runnerScaleSet, nil),
				scalefake.WithGetRunner(runner, nil),
				scalefake.WithGetRunnerByName(runner, nil),
				scalefake.WithGenerateJitRunnerConfig(
					&scaleset.RunnerScaleSetJitRunnerConfig{
						Runner:           runner,
						EncodedJITConfig: "fake-jit-config",
					},
					nil,
				),
			),
		),
	)
}

func NewMinimalAutoscalingRunnerSet(namespace, name string) *actionsv1alpha1.AutoscalingRunnerSet {
	minRunners := 0
	maxRunners := 1

	return &actionsv1alpha1.AutoscalingRunnerSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: actionsv1alpha1.GroupVersion.String(),
			Kind:       "AutoscalingRunnerSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: actionsv1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl:    benchmarkGitHubConfigURL,
			GitHubConfigSecret: benchmarkGitHubConfigSecret,
			RunnerGroup:        "default",
			MinRunners:         &minRunners,
			MaxRunners:         &maxRunners,
			Template:           benchmarkRunnerTemplate(),
		},
	}
}

func NewMinimalAutoscalingListener(namespace, name string) *actionsv1alpha1.AutoscalingListener {
	return &actionsv1alpha1.AutoscalingListener{
		TypeMeta: metav1.TypeMeta{
			APIVersion: actionsv1alpha1.GroupVersion.String(),
			Kind:       "AutoscalingListener",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: actionsv1alpha1.AutoscalingListenerSpec{
			GitHubConfigURL:    benchmarkGitHubConfigURL,
			GitHubConfigSecret: benchmarkGitHubConfigSecret,
			RunnerScaleSetID:   1,
			MaxRunners:         1,
			Image:              benchmarkRunnerImage,
		},
	}
}

func NewMinimalEphemeralRunnerSet(namespace, name string) *actionsv1alpha1.EphemeralRunnerSet {
	return &actionsv1alpha1.EphemeralRunnerSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: actionsv1alpha1.GroupVersion.String(),
			Kind:       "EphemeralRunnerSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: actionsv1alpha1.EphemeralRunnerSetSpec{
			Replicas: 1,
			PatchID:  1,
			EphemeralRunnerSpec: actionsv1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    benchmarkGitHubConfigURL,
				GitHubConfigSecret: benchmarkGitHubConfigSecret,
				RunnerScaleSetID:   1,
				PodTemplateSpec:    benchmarkRunnerTemplate(),
			},
		},
	}
}

func NewMinimalEphemeralRunner(namespace, name string) *actionsv1alpha1.EphemeralRunner {
	return &actionsv1alpha1.EphemeralRunner{
		TypeMeta: metav1.TypeMeta{
			APIVersion: actionsv1alpha1.GroupVersion.String(),
			Kind:       "EphemeralRunner",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: actionsv1alpha1.EphemeralRunnerSpec{
			GitHubConfigURL:    benchmarkGitHubConfigURL,
			GitHubConfigSecret: benchmarkGitHubConfigSecret,
			RunnerScaleSetID:   1,
			PodTemplateSpec:    benchmarkRunnerTemplate(),
		},
	}
}

func benchmarkRunnerTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  actionsv1alpha1.EphemeralRunnerContainerName,
					Image: benchmarkRunnerImage,
				},
			},
		},
	}
}
