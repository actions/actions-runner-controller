package hookdeliveryforwarder

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/actions/actions-runner-controller/github"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Rules        StringSlice
	MetricsAddr  string
	GitHubConfig github.Config
	Checkpointer Checkpointer
}

func (config *Config) InitFlags(fs *flag.FlagSet) {
	if err := envconfig.Process("github", &config.GitHubConfig); err != nil {
		fmt.Fprintln(os.Stderr, "Error: Environment variable read failed.")
	}

	flag.StringVar(&config.MetricsAddr, "metrics-addr", ":8000", "The address the metric endpoint binds to.")
	flag.Var(&config.Rules, "rule", "The rule denotes from where webhook deliveries forwarded and to where they are forwarded. Must be formatted REPO=TARGET where REPO can be just the organization name for a repostory hook or \"owner/repo\" for a repository hook.")
	flag.StringVar(&config.GitHubConfig.Token, "github-token", config.GitHubConfig.Token, "The personal access token of GitHub.")
	flag.Int64Var(&config.GitHubConfig.AppID, "github-app-id", config.GitHubConfig.AppID, "The application ID of GitHub App.")
	flag.Int64Var(&config.GitHubConfig.AppInstallationID, "github-app-installation-id", config.GitHubConfig.AppInstallationID, "The installation ID of GitHub App.")
	flag.StringVar(&config.GitHubConfig.AppPrivateKey, "github-app-private-key", config.GitHubConfig.AppPrivateKey, "The path of a private key file to authenticate as a GitHub App")
}

func Run(ctx context.Context, config *Config) {
	c := config.GitHubConfig

	ghClient, err := c.NewClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: Client creation failed.", err)
		os.Exit(1)
	}

	var wg sync.WaitGroup

	ctx, cancel := context.WithCancel(ctx)

	fwd, err := New(ghClient, []string(config.Rules))
	if err != nil {
		fmt.Fprintf(os.Stderr, "problem initializing forwarder: %v\n", err)
		os.Exit(1)
	}

	if config.Checkpointer != nil {
		fwd.Checkpointer = config.Checkpointer
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", fwd.HandleReadyz)

	srv := http.Server{
		Addr:    config.MetricsAddr,
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
		<-ctx.Done()
		cancel()
	}()

	wg.Wait()
}

type StringSlice []string

func (s *StringSlice) String() string {
	if s == nil {
		return ""
	}

	return fmt.Sprintf("%+v", []string(*s))
}

func (s *StringSlice) Set(value string) error {
	*s = append(*s, value)
	return nil
}
