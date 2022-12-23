package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/actions/actions-runner-controller/github"
	"github.com/actions/actions-runner-controller/pkg/githubwebhookdeliveryforwarder"
	"github.com/kelseyhightower/envconfig"
)

func main() {
	var (
		metricsAddr string
		target      string
		repo        string
	)

	var c github.Config

	if err := envconfig.Process("github", &c); err != nil {
		fmt.Fprintln(os.Stderr, "Error: Environment variable read failed.")
	}

	flag.StringVar(&metricsAddr, "metrics-addr", ":8000", "The address the metric endpoint binds to.")
	flag.StringVar(&repo, "repo", "", "The owner/name of the repository that has the target hook. If specified, the forwarder will use the first hook configured on the repository as the source.")
	flag.StringVar(&target, "target", "", "The URL of the forwarding target that receives all the forwarded webhooks.")
	flag.StringVar(&c.Token, "github-token", c.Token, "The personal access token of GitHub.")
	flag.Int64Var(&c.AppID, "github-app-id", c.AppID, "The application ID of GitHub App.")
	flag.Int64Var(&c.AppInstallationID, "github-app-installation-id", c.AppInstallationID, "The installation ID of GitHub App.")
	flag.StringVar(&c.AppPrivateKey, "github-app-private-key", c.AppPrivateKey, "The path of a private key file to authenticate as a GitHub App")
	flag.Parse()

	ghClient, err := c.NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Client creation failed.", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(context.Background())

	fwd := githubwebhookdeliveryforwarder.New(ghClient, target)
	fwd.Repo = repo

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", fwd.HandleReadyz)

	srv := http.Server{
		Addr:    metricsAddr,
		Handler: mux,
	}

	wg.Add(1)
	go func() {
		defer cancel()
		defer wg.Done()

		if err := fwd.Run(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "problem running forwarder: %v\n", err)
		}
	}()

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
				fmt.Fprintf(os.Stderr, "problem running http server: %v\n", err)
			}
		}
	}()

	go func() {
		<-SetupSignalHandler().Done()
		cancel()
	}()

	wg.Wait()
}

/*
Copyright 2017 The Kubernetes Authors.

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

var onlyOneSignalHandler = make(chan struct{})

var shutdownSignals = []os.Signal{os.Interrupt, syscall.SIGTERM}

// SetupSignalHandler registers for SIGTERM and SIGINT. A stop channel is returned
// which is closed on one of these signals. If a second signal is caught, the program
// is terminated with exit code 1.
func SetupSignalHandler() context.Context {
	close(onlyOneSignalHandler) // panics when called twice

	ctx, cancel := context.WithCancel(context.Background())

	c := make(chan os.Signal, 2)
	signal.Notify(c, shutdownSignals...)
	go func() {
		<-c
		cancel()
		<-c
		os.Exit(1) // second signal. Exit directly.
	}()

	return ctx
}
