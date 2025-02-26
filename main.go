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

	githubv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	summerwindv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	"github.com/actions/actions-runner-controller/build"
	actionsgithubcom "github.com/actions/actions-runner-controller/controllers/actions.github.com"
	actionsgithubcommetrics "github.com/actions/actions-runner-controller/controllers/actions.github.com/metrics"
	actionssummerwindnet "github.com/actions/actions-runner-controller/controllers/actions.summerwind.net"
	"github.com/actions/actions-runner-controller/github"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	// +kubebuilder:scaffold:imports
)

const (
	defaultRunnerImage = "summerwind/actions-runner:latest"
	defaultDockerImage = "docker:dind"
	defaultDockerGID   = "1001"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = githubv1alpha1.AddToScheme(scheme)
	_ = summerwindv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

type stringSlice []string

func (i *stringSlice) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *stringSlice) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func main() {
	var (
		err      error
		ghClient *github.Client

		// metrics server configuration for AutoscalingListener
		listenerMetricsAddr     string
		listenerMetricsEndpoint string

		metricsAddr              string
		autoScalingRunnerSetOnly bool
		enableLeaderElection     bool
		disableAdmissionWebhook  bool
		updateStrategy           string
		leaderElectionId         string
		port                     int
		syncPeriod               time.Duration

		defaultScaleDownDelay time.Duration

		runnerImagePullSecrets stringSlice
		runnerPodDefaults      actionssummerwindnet.RunnerPodDefaults

		namespace                                     string
		logLevel                                      string
		logFormat                                     string
		watchSingleNamespace                          string
		readGithubConfigSecretFromControllerNamespace bool
		excludeLabelPropagationPrefixes               stringSlice

		autoScalerImagePullSecrets stringSlice

		opts = actionsgithubcom.OptionsWithDefault()

		commonRunnerLabels commaSeparatedStringSlice

		k8sClientRateLimiterQPS   int
		k8sClientRateLimiterBurst int
	)
	var c github.Config
	err = envconfig.Process("github", &c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables: %v\n", err)
		os.Exit(1)
	}

	flag.StringVar(&listenerMetricsAddr, "listener-metrics-addr", ":8080", "The address applied to AutoscalingListener metrics server")
	flag.StringVar(&listenerMetricsEndpoint, "listener-metrics-endpoint", "/metrics", "The AutoscalingListener metrics server endpoint from which the metrics are collected")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&leaderElectionId, "leader-election-id", "actions-runner-controller", "Controller id for leader election.")
	flag.StringVar(&runnerPodDefaults.RunnerImage, "runner-image", defaultRunnerImage, "The image name of self-hosted runner container to use by default if one isn't defined in yaml.")
	flag.StringVar(&runnerPodDefaults.DockerImage, "docker-image", defaultDockerImage, "The image name of docker sidecar container to use by default if one isn't defined in yaml.")
	flag.StringVar(&runnerPodDefaults.DockerGID, "docker-gid", defaultDockerGID, "The default GID of docker group in the docker sidecar container. Use 1001 for dockerd sidecars of Ubuntu 20.04 runners 121 for Ubuntu 22.04.")
	flag.Var(&runnerImagePullSecrets, "runner-image-pull-secret", "The default image-pull secret name for self-hosted runner container.")
	flag.StringVar(&runnerPodDefaults.DockerRegistryMirror, "docker-registry-mirror", "", "The default Docker Registry Mirror used by runners.")
	flag.StringVar(&c.Token, "github-token", c.Token, "The personal access token of GitHub.")
	flag.StringVar(&c.EnterpriseURL, "github-enterprise-url", c.EnterpriseURL, "Enterprise URL to be used for your GitHub API calls")
	flag.Int64Var(&c.AppID, "github-app-id", c.AppID, "The application ID of GitHub App.")
	flag.Int64Var(&c.AppInstallationID, "github-app-installation-id", c.AppInstallationID, "The installation ID of GitHub App.")
	flag.StringVar(&c.AppPrivateKey, "github-app-private-key", c.AppPrivateKey, "The path of a private key file to authenticate as a GitHub App")
	flag.StringVar(&c.URL, "github-url", c.URL, "GitHub URL to be used for GitHub API calls")
	flag.StringVar(&c.UploadURL, "github-upload-url", c.UploadURL, "GitHub Upload URL to be used for GitHub API calls")
	flag.StringVar(&c.BasicauthUsername, "github-basicauth-username", c.BasicauthUsername, "Username for GitHub basic auth to use instead of PAT or GitHub APP in case it's running behind a proxy API")
	flag.StringVar(&c.BasicauthPassword, "github-basicauth-password", c.BasicauthPassword, "Password for GitHub basic auth to use instead of PAT or GitHub APP in case it's running behind a proxy API")
	flag.StringVar(&c.RunnerGitHubURL, "runner-github-url", c.RunnerGitHubURL, "GitHub URL to be used by runners during registration")
	flag.BoolVar(&runnerPodDefaults.UseRunnerStatusUpdateHook, "runner-status-update-hook", false, "Use custom RBAC for runners (role, role binding and service account).")
	flag.DurationVar(&defaultScaleDownDelay, "default-scale-down-delay", actionssummerwindnet.DefaultScaleDownDelay, "The approximate delay for a scale down followed by a scale up, used to prevent flapping (down->up->down->... loop)")
	flag.IntVar(&port, "port", 9443, "The port to which the admission webhook endpoint should bind")
	flag.DurationVar(&syncPeriod, "sync-period", 1*time.Minute, "Determines the minimum frequency at which K8s resources managed by this controller are reconciled.")
	flag.IntVar(&opts.RunnerMaxConcurrentReconciles, "runner-max-concurrent-reconciles", opts.RunnerMaxConcurrentReconciles, "The maximum number of concurrent reconciles which can be run by the EphemeralRunner controller. Increase this value to improve the throughput of the controller, but it may also increase the load on the API server and the external service (e.g. GitHub API).")
	flag.Var(&commonRunnerLabels, "common-runner-labels", "Runner labels in the K1=V1,K2=V2,... format that are inherited all the runners created by the controller. See https://github.com/actions/actions-runner-controller/issues/321 for more information")
	flag.StringVar(&namespace, "watch-namespace", "", "The namespace to watch for custom resources. Set to empty for letting it watch for all namespaces.")
	flag.StringVar(&watchSingleNamespace, "watch-single-namespace", "", "Restrict to watch for custom resources in a single namespace.")
	flag.BoolVar(&readGithubConfigSecretFromControllerNamespace, "read-github-config-secret-from-controller-namespace", false, "Read AutoscalingRunnerSet githubConfigSecret from controller namespace.")
	flag.Var(&excludeLabelPropagationPrefixes, "exclude-label-propagation-prefix", "The list of prefixes that should be excluded from label propagation")
	flag.StringVar(&logLevel, "log-level", logging.LogLevelDebug, `The verbosity of the logging. Valid values are "debug", "info", "warn", "error". Defaults to "debug".`)
	flag.StringVar(&logFormat, "log-format", "text", `The log format. Valid options are "text" and "json". Defaults to "text"`)
	flag.BoolVar(&autoScalingRunnerSetOnly, "auto-scaling-runner-set-only", false, "Make controller only reconcile AutoRunnerScaleSet object.")
	flag.StringVar(&updateStrategy, "update-strategy", "immediate", `Resources reconciliation strategy on upgrade with running/pending jobs. Valid values are: "immediate", "eventual". Defaults to "immediate".`)
	flag.Var(&autoScalerImagePullSecrets, "auto-scaler-image-pull-secrets", "The default image-pull secret name for auto-scaler listener container.")
	flag.IntVar(&k8sClientRateLimiterQPS, "k8s-client-rate-limiter-qps", 20, "The QPS value of the K8s client rate limiter.")
	flag.IntVar(&k8sClientRateLimiterBurst, "k8s-client-rate-limiter-burst", 30, "The burst value of the K8s client rate limiter.")
	flag.Parse()

	runnerPodDefaults.RunnerImagePullSecrets = runnerImagePullSecrets

	log, err := logging.NewLogger(logLevel, logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating logger: %v\n", err)
		os.Exit(1)
	}
	c.Log = &log

	log.Info("Using options", "runner-max-concurrent-reconciles", opts.RunnerMaxConcurrentReconciles)

	if !autoScalingRunnerSetOnly {
		ghClient, err = c.NewClient()
		if err != nil {
			log.Error(err, "unable to create client")
			os.Exit(1)
		}
	}

	ctrl.SetLogger(log)

	managerNamespace := ""
	var defaultNamespaces map[string]cache.Config
	if namespace != "" {
		defaultNamespaces = map[string]cache.Config{
			namespace: {},
		}
	}

	if autoScalingRunnerSetOnly {
		managerNamespace = os.Getenv("CONTROLLER_MANAGER_POD_NAMESPACE")
		if managerNamespace == "" {
			log.Error(err, "unable to obtain manager pod namespace")
			os.Exit(1)
		}

		if len(watchSingleNamespace) > 0 {
			if defaultNamespaces == nil {
				defaultNamespaces = make(map[string]cache.Config)
			}

			defaultNamespaces[watchSingleNamespace] = cache.Config{}
			defaultNamespaces[managerNamespace] = cache.Config{}
		}

		switch updateStrategy {
		case "eventual", "immediate":
			log.Info(`Update strategy set to:`, "updateStrategy", updateStrategy)
		default:
			log.Info(`Update strategy not recognized. Defaulting to "immediately"`, "updateStrategy", updateStrategy)
			updateStrategy = "immediate"
		}
	}

	if actionsgithubcom.SetListenerLoggingParameters(logLevel, logFormat) {
		log.Info("AutoscalingListener logging parameters changed", "LogLevel", logLevel, "LogFormat", logFormat)
	} else {
		log.Info("Using default AutoscalingListener logging parameters", "LogLevel", actionsgithubcom.DefaultScaleSetListenerLogLevel, "LogFormat", actionsgithubcom.DefaultScaleSetListenerLogFormat)
	}

	actionsgithubcom.SetListenerEntrypoint(os.Getenv("LISTENER_ENTRYPOINT"))

	var webhookServer webhook.Server
	if port != 0 {
		webhookServer = webhook.NewServer(webhook.Options{
			Port: port,
		})
	}

	cfg := ctrl.GetConfigOrDie()
	cfg.QPS = float32(k8sClientRateLimiterQPS)
	cfg.Burst = k8sClientRateLimiterBurst

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		Cache: cache.Options{
			SyncPeriod:        &syncPeriod,
			DefaultNamespaces: defaultNamespaces,
		},
		WebhookServer:    webhookServer,
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: leaderElectionId,
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{
					&corev1.Secret{},
					&corev1.ConfigMap{},
				},
			},
		},
	})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if autoScalingRunnerSetOnly {
		if err := actionsgithubcom.SetupIndexers(mgr); err != nil {
			log.Error(err, "unable to setup indexers")
			os.Exit(1)
		}
		managerImage := os.Getenv("CONTROLLER_MANAGER_CONTAINER_IMAGE")
		if managerImage == "" {
			log.Error(err, "unable to obtain listener image")
			os.Exit(1)
		}

		if metricsAddr != "" {
			log.Info("Registering scale set metrics")
			actionsgithubcommetrics.RegisterMetrics()
		}

		actionsMultiClient := actions.NewMultiClient(
			log.WithName("actions-clients"),
		)

		rb := actionsgithubcom.ResourceBuilder{
			ExcludeLabelPropagationPrefixes: excludeLabelPropagationPrefixes,
		}

		if err = (&actionsgithubcom.AutoscalingRunnerSetReconciler{
			Client:              mgr.GetClient(),
			Log:                 log.WithName("AutoscalingRunnerSet").WithValues("version", build.Version),
			Scheme:              mgr.GetScheme(),
			ControllerNamespace: managerNamespace,
			ReadGitHubConfigSecretFromControllerNamespace: readGithubConfigSecretFromControllerNamespace,
			DefaultRunnerScaleSetListenerImage:            managerImage,
			ActionsClient:                                 actionsMultiClient,
			UpdateStrategy:                                actionsgithubcom.UpdateStrategy(updateStrategy),
			DefaultRunnerScaleSetListenerImagePullSecrets: autoScalerImagePullSecrets,
			ResourceBuilder:                               rb,
		}).SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "AutoscalingRunnerSet")
			os.Exit(1)
		}

		if err = (&actionsgithubcom.EphemeralRunnerReconciler{
			Client:              mgr.GetClient(),
			Log:                 log.WithName("EphemeralRunner").WithValues("version", build.Version),
			Scheme:              mgr.GetScheme(),
			ActionsClient:       actionsMultiClient,
			ControllerNamespace: managerNamespace,
			ReadGitHubConfigSecretFromControllerNamespace: readGithubConfigSecretFromControllerNamespace,
			ResourceBuilder: rb,
		}).SetupWithManager(mgr, actionsgithubcom.WithMaxConcurrentReconciles(opts.RunnerMaxConcurrentReconciles)); err != nil {
			log.Error(err, "unable to create controller", "controller", "EphemeralRunner")
			os.Exit(1)
		}

		if err = (&actionsgithubcom.EphemeralRunnerSetReconciler{
			Client:              mgr.GetClient(),
			Log:                 log.WithName("EphemeralRunnerSet").WithValues("version", build.Version),
			Scheme:              mgr.GetScheme(),
			ActionsClient:       actionsMultiClient,
			PublishMetrics:      metricsAddr != "0",
			ControllerNamespace: managerNamespace,
			ReadGitHubConfigSecretFromControllerNamespace: readGithubConfigSecretFromControllerNamespace,
			ResourceBuilder: rb,
		}).SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "EphemeralRunnerSet")
			os.Exit(1)
		}

		if err = (&actionsgithubcom.AutoscalingListenerReconciler{
			Client: mgr.GetClient(),
			Log:    log.WithName("AutoscalingListener").WithValues("version", build.Version),
			Scheme: mgr.GetScheme(),
			ReadGitHubConfigSecretFromControllerNamespace: readGithubConfigSecretFromControllerNamespace,
			ListenerMetricsAddr:                           listenerMetricsAddr,
			ListenerMetricsEndpoint:                       listenerMetricsEndpoint,
			ResourceBuilder:                               rb,
		}).SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "AutoscalingListener")
			os.Exit(1)
		}
	} else {
		multiClient := actionssummerwindnet.NewMultiGitHubClient(
			mgr.GetClient(),
			ghClient,
		)

		runnerReconciler := &actionssummerwindnet.RunnerReconciler{
			Client:            mgr.GetClient(),
			Log:               log.WithName("runner"),
			Scheme:            mgr.GetScheme(),
			GitHubClient:      multiClient,
			RunnerPodDefaults: runnerPodDefaults,
		}

		if err = runnerReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "Runner")
			os.Exit(1)
		}

		runnerReplicaSetReconciler := &actionssummerwindnet.RunnerReplicaSetReconciler{
			Client: mgr.GetClient(),
			Log:    log.WithName("runnerreplicaset"),
			Scheme: mgr.GetScheme(),
		}

		if err = runnerReplicaSetReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "RunnerReplicaSet")
			os.Exit(1)
		}

		runnerDeploymentReconciler := &actionssummerwindnet.RunnerDeploymentReconciler{
			Client:             mgr.GetClient(),
			Log:                log.WithName("runnerdeployment"),
			Scheme:             mgr.GetScheme(),
			CommonRunnerLabels: commonRunnerLabels,
		}

		if err = runnerDeploymentReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "RunnerDeployment")
			os.Exit(1)
		}

		runnerSetReconciler := &actionssummerwindnet.RunnerSetReconciler{
			Client:             mgr.GetClient(),
			Log:                log.WithName("runnerset"),
			Scheme:             mgr.GetScheme(),
			CommonRunnerLabels: commonRunnerLabels,
			GitHubClient:       multiClient,
			RunnerPodDefaults:  runnerPodDefaults,
		}

		if err = runnerSetReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "RunnerSet")
			os.Exit(1)
		}

		log.Info(
			"Initializing actions-runner-controller",
			"version", build.Version,
			"default-scale-down-delay", defaultScaleDownDelay,
			"sync-period", syncPeriod,
			"default-runner-image", runnerPodDefaults.RunnerImage,
			"default-docker-image", runnerPodDefaults.DockerImage,
			"default-docker-gid", runnerPodDefaults.DockerGID,
			"common-runnner-labels", commonRunnerLabels,
			"leader-election-enabled", enableLeaderElection,
			"leader-election-id", leaderElectionId,
			"watch-namespace", namespace,
		)

		horizontalRunnerAutoscaler := &actionssummerwindnet.HorizontalRunnerAutoscalerReconciler{
			Client:                mgr.GetClient(),
			Log:                   log.WithName("horizontalrunnerautoscaler"),
			Scheme:                mgr.GetScheme(),
			GitHubClient:          multiClient,
			DefaultScaleDownDelay: defaultScaleDownDelay,
		}

		runnerPodReconciler := &actionssummerwindnet.RunnerPodReconciler{
			Client:       mgr.GetClient(),
			Log:          log.WithName("runnerpod"),
			Scheme:       mgr.GetScheme(),
			GitHubClient: multiClient,
		}

		runnerPersistentVolumeReconciler := &actionssummerwindnet.RunnerPersistentVolumeReconciler{
			Client: mgr.GetClient(),
			Log:    log.WithName("runnerpersistentvolume"),
			Scheme: mgr.GetScheme(),
		}

		runnerPersistentVolumeClaimReconciler := &actionssummerwindnet.RunnerPersistentVolumeClaimReconciler{
			Client: mgr.GetClient(),
			Log:    log.WithName("runnerpersistentvolumeclaim"),
			Scheme: mgr.GetScheme(),
		}

		if err = runnerPodReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "RunnerPod")
			os.Exit(1)
		}

		if err = horizontalRunnerAutoscaler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "HorizontalRunnerAutoscaler")
			os.Exit(1)
		}

		if err = runnerPersistentVolumeReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "RunnerPersistentVolume")
			os.Exit(1)
		}

		if err = runnerPersistentVolumeClaimReconciler.SetupWithManager(mgr); err != nil {
			log.Error(err, "unable to create controller", "controller", "RunnerPersistentVolumeClaim")
			os.Exit(1)
		}

		if !disableAdmissionWebhook {
			if err = (&summerwindv1alpha1.Runner{}).SetupWebhookWithManager(mgr); err != nil {
				log.Error(err, "unable to create webhook", "webhook", "Runner")
				os.Exit(1)
			}
			if err = (&summerwindv1alpha1.RunnerDeployment{}).SetupWebhookWithManager(mgr); err != nil {
				log.Error(err, "unable to create webhook", "webhook", "RunnerDeployment")
				os.Exit(1)
			}
			if err = (&summerwindv1alpha1.RunnerReplicaSet{}).SetupWebhookWithManager(mgr); err != nil {
				log.Error(err, "unable to create webhook", "webhook", "RunnerReplicaSet")
				os.Exit(1)
			}
			injector := &actionssummerwindnet.PodRunnerTokenInjector{
				Client:       mgr.GetClient(),
				GitHubClient: multiClient,
				Log:          ctrl.Log.WithName("webhook").WithName("PodRunnerTokenInjector"),
			}
			if err = injector.SetupWithManager(mgr); err != nil {
				log.Error(err, "unable to create webhook server", "webhook", "PodRunnerTokenInjector")
				os.Exit(1)
			}
		}
	}

	log.Info("starting manager", "version", build.Version)
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
