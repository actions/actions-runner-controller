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
	"strings"
	"time"

	actionsv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/controllers"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/kelseyhightower/envconfig"
	zaplib "go.uber.org/zap"
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

	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
)

var (
	scheme = runtime.NewScheme()
	log    = ctrl.Log.WithName("actions-runner-controller")
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

		gitHubAPICacheDuration time.Duration

		runnerImage          string
		dockerImage          string
		dockerRegistryMirror string
		namespace            string
		logLevel             string

		commonRunnerLabels commaSeparatedStringSlice
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
	flag.StringVar(&dockerRegistryMirror, "docker-registry-mirror", "", "The default Docker Registry Mirror used by runners.")
	flag.StringVar(&c.Token, "github-token", c.Token, "The personal access token of GitHub.")
	flag.Int64Var(&c.AppID, "github-app-id", c.AppID, "The application ID of GitHub App.")
	flag.Int64Var(&c.AppInstallationID, "github-app-installation-id", c.AppInstallationID, "The installation ID of GitHub App.")
	flag.StringVar(&c.AppPrivateKey, "github-app-private-key", c.AppPrivateKey, "The path of a private key file to authenticate as a GitHub App")
	flag.DurationVar(&gitHubAPICacheDuration, "github-api-cache-duration", 0, "The duration until the GitHub API cache expires. Setting this to e.g. 10m results in the controller tries its best not to make the same API call within 10m to reduce the chance of being rate-limited. Defaults to mostly the same value as sync-period. If you're tweaking this in order to make autoscaling more responsive, you'll probably want to tweak sync-period, too")
	flag.DurationVar(&syncPeriod, "sync-period", 10*time.Minute, "Determines the minimum frequency at which K8s resources managed by this controller are reconciled. When you use autoscaling, set to a lower value like 10 minute, because this corresponds to the minimum time to react on demand change. . If you're tweaking this in order to make autoscaling more responsive, you'll probably want to tweak github-api-cache-duration, too")
	flag.Var(&commonRunnerLabels, "common-runner-labels", "Runner labels in the K1=V1,K2=V2,... format that are inherited all the runners created by the controller. See https://github.com/actions-runner-controller/actions-runner-controller/issues/321 for more information")
	flag.StringVar(&namespace, "watch-namespace", "", "The namespace to watch for custom resources. Set to empty for letting it watch for all namespaces.")
	flag.StringVar(&logLevel, "log-level", logLevelDebug, `The verbosity of the logging. Valid values are "debug", "info", "warn", "error". Defaults to "debug".`)
	flag.Parse()

	logger := zap.New(func(o *zap.Options) {
		switch logLevel {
		case logLevelDebug:
			o.Development = true
		case logLevelInfo:
			lvl := zaplib.NewAtomicLevelAt(zaplib.InfoLevel)
			o.Level = &lvl
		case logLevelWarn:
			lvl := zaplib.NewAtomicLevelAt(zaplib.WarnLevel)
			o.Level = &lvl
		case logLevelError:
			lvl := zaplib.NewAtomicLevelAt(zaplib.ErrorLevel)
			o.Level = &lvl
		}
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
		LeaderElectionID:   "actions-runner-controller",
		Port:               9443,
		SyncPeriod:         &syncPeriod,
		Namespace:          namespace,
	})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	runnerReconciler := &controllers.RunnerReconciler{
		Client:               mgr.GetClient(),
		Log:                  log.WithName("runner"),
		Scheme:               mgr.GetScheme(),
		GitHubClient:         ghClient,
		RunnerImage:          runnerImage,
		DockerImage:          dockerImage,
		DockerRegistryMirror: dockerRegistryMirror,
	}

	if err = runnerReconciler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "Runner")
		os.Exit(1)
	}

	runnerReplicaSetReconciler := &controllers.RunnerReplicaSetReconciler{
		Client:       mgr.GetClient(),
		Log:          log.WithName("runnerreplicaset"),
		Scheme:       mgr.GetScheme(),
		GitHubClient: ghClient,
	}

	if err = runnerReplicaSetReconciler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "RunnerReplicaSet")
		os.Exit(1)
	}

	runnerDeploymentReconciler := &controllers.RunnerDeploymentReconciler{
		Client:             mgr.GetClient(),
		Log:                log.WithName("runnerdeployment"),
		Scheme:             mgr.GetScheme(),
		CommonRunnerLabels: commonRunnerLabels,
	}

	if err = runnerDeploymentReconciler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "RunnerDeployment")
		os.Exit(1)
	}

	runnerSetReconciler := &controllers.RunnerSetReconciler{
		Client:               mgr.GetClient(),
		Log:                  log.WithName("runnerset"),
		Scheme:               mgr.GetScheme(),
		CommonRunnerLabels:   commonRunnerLabels,
		RunnerImage:          runnerImage,
		DockerImage:          dockerImage,
		DockerRegistryMirror: dockerRegistryMirror,
		GitHubBaseURL:        ghClient.GithubBaseURL,
	}

	if err = runnerSetReconciler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "RunnerSet")
		os.Exit(1)
	}
	if gitHubAPICacheDuration == 0 {
		gitHubAPICacheDuration = syncPeriod - 10*time.Second
	}

	if gitHubAPICacheDuration < 0 {
		gitHubAPICacheDuration = 0
	}

	log.Info(
		"Initializing actions-runner-controller",
		"github-api-cahce-duration", gitHubAPICacheDuration,
		"sync-period", syncPeriod,
		"runner-image", runnerImage,
		"docker-image", dockerImage,
		"common-runnner-labels", commonRunnerLabels,
		"watch-namespace", namespace,
	)

	horizontalRunnerAutoscaler := &controllers.HorizontalRunnerAutoscalerReconciler{
		Client:        mgr.GetClient(),
		Log:           log.WithName("horizontalrunnerautoscaler"),
		Scheme:        mgr.GetScheme(),
		GitHubClient:  ghClient,
		CacheDuration: gitHubAPICacheDuration,
	}

	runnerPodReconciler := &controllers.RunnerPodReconciler{
		Client:       mgr.GetClient(),
		Log:          log.WithName("runnerpod"),
		Scheme:       mgr.GetScheme(),
		GitHubClient: ghClient,
	}

	if err = runnerPodReconciler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "RunnerPod")
		os.Exit(1)
	}

	if err = horizontalRunnerAutoscaler.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create controller", "controller", "HorizontalRunnerAutoscaler")
		os.Exit(1)
	}

	if err = (&actionsv1alpha1.Runner{}).SetupWebhookWithManager(mgr); err != nil {
		log.Error(err, "unable to create webhook", "webhook", "Runner")
		os.Exit(1)
	}
	if err = (&actionsv1alpha1.RunnerDeployment{}).SetupWebhookWithManager(mgr); err != nil {
		log.Error(err, "unable to create webhook", "webhook", "RunnerDeployment")
		os.Exit(1)
	}
	if err = (&actionsv1alpha1.RunnerReplicaSet{}).SetupWebhookWithManager(mgr); err != nil {
		log.Error(err, "unable to create webhook", "webhook", "RunnerReplicaSet")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	injector := &controllers.PodRunnerTokenInjector{
		Client:       mgr.GetClient(),
		GitHubClient: ghClient,
		Log:          ctrl.Log.WithName("webhook").WithName("PodRunnerTokenInjector"),
	}
	if err = injector.SetupWithManager(mgr); err != nil {
		log.Error(err, "unable to create webhook server", "webhook", "PodRunnerTokenInjector")
		os.Exit(1)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

type commaSeparatedStringSlice []string

func (s *commaSeparatedStringSlice) String() string {
	return fmt.Sprintf("%v", *s)
}

func (s *commaSeparatedStringSlice) Set(value string) error {
	for _, v := range strings.Split(value, ",") {
		if v == "" {
			continue
		}

		*s = append(*s, v)
	}
	return nil
}
