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

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	actionsv1alpha1 "github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/controllers"
	"github.com/summerwind/actions-runner-controller/github"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

const (
	defaultRunnerImage = "summerwind/actions-runner:latest"
	defaultDockerImage = "docker:dind"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = actionsv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var (
		err      error
		ghClient *github.Client

		metricsAddr          string
		enableLeaderElection bool
		syncPeriod           time.Duration

		runnerImage string
		dockerImage string
	)

	var c github.Config
	err = envconfig.Process("github", &c)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Environment variable read failed.")
	}

	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&runnerImage, "runner-image", defaultRunnerImage, "The image name of self-hosted runner container.")
	flag.StringVar(&dockerImage, "docker-image", defaultDockerImage, "The image name of docker sidecar container.")
	flag.StringVar(&c.Token, "github-token", c.Token, "The personal access token of GitHub.")
	flag.Int64Var(&c.AppID, "github-app-id", c.AppID, "The application ID of GitHub App.")
	flag.Int64Var(&c.AppInstallationID, "github-app-installation-id", c.AppInstallationID, "The installation ID of GitHub App.")
	flag.StringVar(&c.AppPrivateKey, "github-app-private-key", c.AppPrivateKey, "The path of a private key file to authenticate as a GitHub App")
	flag.DurationVar(&syncPeriod, "sync-period", 10*time.Minute, "Determines the minimum frequency at which K8s resources managed by this controller are reconciled. When you use autoscaling, set to a lower value like 10 minute, because this corresponds to the minimum time to react on demand change")
	flag.Parse()

	logger := zap.New(func(o *zap.Options) {
		o.Development = true
	})

	ghClient, err = c.NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Client creation failed.", err)
		os.Exit(1)
	}

	ctrl.SetLogger(logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		LeaderElection:     enableLeaderElection,
		Port:               9443,
		SyncPeriod:         &syncPeriod,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	runnerReconciler := &controllers.RunnerReconciler{
		Client:       mgr.GetClient(),
		Log:          ctrl.Log.WithName("controllers").WithName("Runner"),
		Scheme:       mgr.GetScheme(),
		GitHubClient: ghClient,
		RunnerImage:  runnerImage,
		DockerImage:  dockerImage,
	}

	if err = runnerReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Runner")
		os.Exit(1)
	}

	runnerSetReconciler := &controllers.RunnerReplicaSetReconciler{
		Client:       mgr.GetClient(),
		Log:          ctrl.Log.WithName("controllers").WithName("RunnerReplicaSet"),
		Scheme:       mgr.GetScheme(),
		GitHubClient: ghClient,
	}

	if err = runnerSetReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RunnerReplicaSet")
		os.Exit(1)
	}

	runnerDeploymentReconciler := &controllers.RunnerDeploymentReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("RunnerDeployment"),
		Scheme: mgr.GetScheme(),
	}

	if err = runnerDeploymentReconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RunnerDeployment")
		os.Exit(1)
	}

	horizontalRunnerAutoscaler := &controllers.HorizontalRunnerAutoscalerReconciler{
		Client:       mgr.GetClient(),
		Log:          ctrl.Log.WithName("controllers").WithName("HorizontalRunnerAutoscaler"),
		Scheme:       mgr.GetScheme(),
		GitHubClient: ghClient,
	}

	if err = horizontalRunnerAutoscaler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "HorizontalRunnerAutoscaler")
		os.Exit(1)
	}

	if err = (&actionsv1alpha1.Runner{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "Runner")
		os.Exit(1)
	}
	if err = (&actionsv1alpha1.RunnerDeployment{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "RunnerDeployment")
		os.Exit(1)
	}
	if err = (&actionsv1alpha1.RunnerReplicaSet{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "RunnerReplicaSet")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
