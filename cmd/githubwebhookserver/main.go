/*
Copyright 2021 The actions-runner-controller authors.

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
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	actionsv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
	"github.com/actions-runner-controller/actions-runner-controller/controllers"
	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/kelseyhightower/envconfig"
	zaplib "go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/exec"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"

	webhookSecretTokenEnvName = "GITHUB_WEBHOOK_SECRET_TOKEN"
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = actionsv1alpha1.AddToScheme(scheme)
	// +kubebuilder:scaffold:scheme
}

func main() {
	var (
		err error

		webhookAddr string
		metricsAddr string

		// The secret token of the GitHub Webhook. See https://docs.github.com/en/developers/webhooks-and-events/securing-your-webhooks
		webhookSecretToken    string
		webhookSecretTokenEnv string

		watchNamespace string

		enableLeaderElection bool
		syncPeriod           time.Duration
		logLevel             string

		ghClient *github.Client
	)

	var c github.Config
	err = envconfig.Process("github", &c)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: processing environment variables: %v\n", err)
		os.Exit(1)
	}

	webhookSecretTokenEnv = os.Getenv(webhookSecretTokenEnvName)

	flag.StringVar(&webhookAddr, "webhook-addr", ":8000", "The address the metric endpoint binds to.")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&watchNamespace, "watch-namespace", "", "The namespace to watch for HorizontalRunnerAutoscaler's to scale on Webhook. Set to empty for letting it watch for all namespaces.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.DurationVar(&syncPeriod, "sync-period", 10*time.Minute, "Determines the minimum frequency at which K8s resources managed by this controller are reconciled. When you use autoscaling, set to a lower value like 10 minute, because this corresponds to the minimum time to react on demand change")
	flag.StringVar(&logLevel, "log-level", logLevelDebug, `The verbosity of the logging. Valid values are "debug", "info", "warn", "error". Defaults to "debug".`)
	flag.StringVar(&webhookSecretToken, "github-webhook-secret-token", "", "The personal access token of GitHub.")
	flag.StringVar(&c.Token, "github-token", c.Token, "The personal access token of GitHub.")
	flag.Int64Var(&c.AppID, "github-app-id", c.AppID, "The application ID of GitHub App.")
	flag.Int64Var(&c.AppInstallationID, "github-app-installation-id", c.AppInstallationID, "The installation ID of GitHub App.")
	flag.StringVar(&c.AppPrivateKey, "github-app-private-key", c.AppPrivateKey, "The path of a private key file to authenticate as a GitHub App")
	flag.StringVar(&c.URL, "github-url", c.URL, "GitHub URL to be used for GitHub API calls")
	flag.StringVar(&c.UploadURL, "github-upload-url", c.UploadURL, "GitHub Upload URL to be used for GitHub API calls")
	flag.StringVar(&c.BasicauthUsername, "github-basicauth-username", c.BasicauthUsername, "Username for GitHub basic auth to use instead of PAT or GitHub APP in case it's running behind a proxy API")
	flag.StringVar(&c.BasicauthPassword, "github-basicauth-password", c.BasicauthPassword, "Password for GitHub basic auth to use instead of PAT or GitHub APP in case it's running behind a proxy API")
	flag.StringVar(&c.RunnerGitHubURL, "runner-github-url", c.RunnerGitHubURL, "GitHub URL to be used by runners during registration")

	flag.Parse()

	if webhookSecretToken == "" && webhookSecretTokenEnv != "" {
		setupLog.Info(fmt.Sprintf("Using the value from %s for -github-webhook-secret-token", webhookSecretTokenEnvName))
		webhookSecretToken = webhookSecretTokenEnv
	}

	if webhookSecretToken == "" {
		setupLog.Info(fmt.Sprintf("-github-webhook-secret-token and %s are missing or empty. Create one following https://docs.github.com/en/developers/webhooks-and-events/securing-your-webhooks and specify it via the flag or the envvar", webhookSecretTokenEnvName))
	}

	if watchNamespace == "" {
		setupLog.Info("-watch-namespace is empty. HorizontalRunnerAutoscalers in all the namespaces are watched, cached, and considered as scale targets.")
	} else {
		setupLog.Info("-watch-namespace is %q. Only HorizontalRunnerAutoscalers in %q are watched, cached, and considered as scale targets.")
	}

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

	// In order to support runner groups with custom visibility (selected repositories), we need to perform some GitHub API calls.
	// Let the user define if they want to opt-in supporting this option by providing the proper GitHub authentication parameters
	// Without an opt-in, runner groups with custom visibility won't be supported to save API calls
	// That is, all runner groups managed by ARC are assumed to be visible to any repositories,
	// which is wrong when you have one or more non-default runner groups in your organization or enterprise.
	if len(c.Token) > 0 || (c.AppID > 0 && c.AppInstallationID > 0 && c.AppPrivateKey != "") || (len(c.BasicauthUsername) > 0 && len(c.BasicauthPassword) > 0) {
		ghClient, err = c.NewClient()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: Client creation failed.", err)
			setupLog.Error(err, "unable to create controller", "controller", "Runner")
			os.Exit(1)
		}
	} else {
		setupLog.Info("GitHub client is not initialized. Runner groups with custom visibility are not supported. If needed, please provide GitHub authentication. This will incur in extra GitHub API calls")
	}

	ctrl.SetLogger(logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		SyncPeriod:         &syncPeriod,
		LeaderElection:     enableLeaderElection,
		Namespace:          watchNamespace,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	hraGitHubWebhook := &controllers.HorizontalRunnerAutoscalerGitHubWebhook{
		Client:         mgr.GetClient(),
		Log:            ctrl.Log.WithName("controllers").WithName("Runner"),
		Recorder:       nil,
		Scheme:         mgr.GetScheme(),
		SecretKeyBytes: []byte(webhookSecretToken),
		Namespace:      watchNamespace,
		GitHubClient:   ghClient,
	}

	if err = hraGitHubWebhook.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Runner")
		os.Exit(1)
	}

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())

	wg.Add(1)
	go func() {
		defer cancel()
		defer wg.Done()

		setupLog.Info("starting webhook server")
		if err := mgr.Start(ctx); err != nil {
			setupLog.Error(err, "problem running manager")
			os.Exit(1)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", hraGitHubWebhook.Handle)

	srv := http.Server{
		Addr:    webhookAddr,
		Handler: mux,
	}

	wg.Add(1)
	go func() {
		defer cancel()
		defer wg.Done()

		go func() {
			<-ctx.Done()

			srv.Shutdown(context.Background())
		}()

		if err := srv.ListenAndServe(); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				setupLog.Error(err, "problem running http server")
			}
		}
	}()

	go func() {
		<-ctrl.SetupSignalHandler().Done()
		cancel()
	}()

	wg.Wait()
}
