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
	"crypto/x509"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/actions/actions-runner-controller/build"
	"github.com/actions/actions-runner-controller/cmd/githubrunnerscalesetlistener/config"
	"github.com/actions/actions-runner-controller/github/actions"
	"github.com/actions/actions-runner-controller/logging"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/net/http/httpproxy"
	"golang.org/x/sync/errgroup"
)

func main() {
	configPath, ok := os.LookupEnv("LISTENER_CONFIG_PATH")
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: LISTENER_CONFIG_PATH environment variable is not set\n")
		os.Exit(1)
	}

	rc, err := config.Read(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: reading config from path(%q): %v\n", configPath, err)
		os.Exit(1)
	}

	logLevel := string(logging.LogLevelDebug)
	if rc.LogLevel != "" {
		logLevel = rc.LogLevel
	}

	logFormat := string(logging.LogFormatText)
	if rc.LogFormat != "" {
		logFormat = rc.LogFormat
	}

	logger, err := logging.NewLogger(logLevel, logFormat)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: creating logger: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		opts := runOptions{
			serviceOptions: []func(*Service){
				WithLogger(logger),
			},
		}
		opts.serviceOptions = append(opts.serviceOptions, WithPrometheusMetrics(rc))

		return run(ctx, rc, logger, opts)
	})

	if len(rc.MetricsAddr) != 0 {
		g.Go(func() error {
			metricsServer := metricsServer{
				rc:     rc,
				logger: logger,
			}
			g.Go(func() error {
				<-ctx.Done()
				return metricsServer.shutdown()
			})
			return metricsServer.listenAndServe()
		})
	}

	if err := g.Wait(); err != nil {
		logger.Error(err, "Error encountered")
		os.Exit(1)
	}
}

type metricsServer struct {
	rc     config.Config
	logger logr.Logger
	srv    *http.Server
}

func (s *metricsServer) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

func (s *metricsServer) listenAndServe() error {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		// availableJobs,
		// acquiredJobs,
		assignedJobs,
		runningJobs,
		registeredRunners,
		busyRunners,
		minRunners,
		maxRunners,
		desiredRunners,
		idleRunners,
		startedJobsTotal,
		completedJobsTotal,
		// jobQueueDurationSeconds,
		jobStartupDurationSeconds,
		jobExecutionDurationSeconds,
	)

	mux := http.NewServeMux()
	mux.Handle(
		s.rc.MetricsEndpoint,
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}),
	)

	s.srv = &http.Server{
		Addr:    s.rc.MetricsAddr,
		Handler: mux,
	}

	s.logger.Info("Starting metrics server", "address", s.srv.Addr)
	return s.srv.ListenAndServe()
}

type runOptions struct {
	serviceOptions []func(*Service)
}

func run(ctx context.Context, rc config.Config, logger logr.Logger, opts runOptions) error {
	// Create root context and hook with sigint and sigterm
	creds := &actions.ActionsAuth{}
	if rc.Token != "" {
		creds.Token = rc.Token
	} else {
		creds.AppCreds = &actions.GitHubAppAuth{
			AppID:             rc.AppID,
			AppInstallationID: rc.AppInstallationID,
			AppPrivateKey:     rc.AppPrivateKey,
		}
	}

	actionsServiceClient, err := newActionsClientFromConfig(
		rc,
		creds,
		actions.WithLogger(logger),
	)
	actionsServiceClient.SetUserAgent(actions.UserAgentInfo{
		Version:    build.Version,
		CommitSHA:  build.CommitSHA,
		ScaleSetID: rc.RunnerScaleSetId,
		HasProxy:   hasProxy(),
		Subsystem:  "githubrunnerscalesetlistener",
	})
	if err != nil {
		return fmt.Errorf("failed to create an Actions Service client: %w", err)
	}

	// Create message listener
	autoScalerClient, err := NewAutoScalerClient(ctx, actionsServiceClient, &logger, rc.RunnerScaleSetId)
	if err != nil {
		return fmt.Errorf("failed to create a message listener: %w", err)
	}
	defer autoScalerClient.Close()

	// Create kube manager and scale controller
	kubeManager, err := NewKubernetesManager(&logger)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes manager: %w", err)
	}

	scaleSettings := &ScaleSettings{
		Namespace:    rc.EphemeralRunnerSetNamespace,
		ResourceName: rc.EphemeralRunnerSetName,
		MaxRunners:   rc.MaxRunners,
		MinRunners:   rc.MinRunners,
	}

	service, err := NewService(ctx, autoScalerClient, kubeManager, scaleSettings, opts.serviceOptions...)
	if err != nil {
		return fmt.Errorf("failed to create new service: %v", err)
	}

	// Start listening for messages
	if err = service.Start(); err != nil {
		return fmt.Errorf("failed to start message queue listener: %w", err)
	}
	return nil
}

func newActionsClientFromConfig(config config.Config, creds *actions.ActionsAuth, options ...actions.ClientOption) (*actions.Client, error) {
	if config.ServerRootCA != "" {
		systemPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("failed to load system cert pool: %w", err)
		}
		pool := systemPool.Clone()
		ok := pool.AppendCertsFromPEM([]byte(config.ServerRootCA))
		if !ok {
			return nil, fmt.Errorf("failed to parse root certificate")
		}

		options = append(options, actions.WithRootCAs(pool))
	}

	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	options = append(options, actions.WithProxy(func(req *http.Request) (*url.URL, error) {
		return proxyFunc(req.URL)
	}))

	return actions.NewClient(config.ConfigureUrl, creds, options...)
}

func hasProxy() bool {
	proxyFunc := httpproxy.FromEnvironment().ProxyFunc()
	return proxyFunc != nil
}
