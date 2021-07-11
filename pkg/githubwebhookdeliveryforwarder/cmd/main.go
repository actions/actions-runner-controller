package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/actions-runner-controller/actions-runner-controller/github"
	"github.com/actions-runner-controller/actions-runner-controller/pkg/githubwebhookdeliveryforwarder"
	"github.com/kelseyhightower/envconfig"
)

func main() {
	var (
		rules       stringSlice
		metricsAddr string
	)

	var c github.Config

	if err := envconfig.Process("github", &c); err != nil {
		fmt.Fprintln(os.Stderr, "Error: Environment variable read failed.")
	}

	flag.StringVar(&metricsAddr, "metrics-addr", ":8000", "The address the metric endpoint binds to.")
	flag.Var(&rules, "rule", "The rule denotes from where webhook deliveries forwarded and to where they are forwarded. Must be formatted REPO=TARGET where REPO can be just the organization name for a repostory hook or \"owner/repo\" for a repository hook.")
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

	fwd, err := githubwebhookdeliveryforwarder.New(ghClient, []string(rules))
	if err != nil {
		fmt.Fprintf(os.Stderr, "problem initializing forwarder: %v\n", err)
		os.Exit(1)
	}

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

type stringSlice []string

func (s *stringSlice) String() string {
	if s == nil {
		return ""
	}

	return fmt.Sprintf("%+v", []string(*s))
}

func (s *stringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}
