package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/app"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	configPath, ok := os.LookupEnv("LISTENER_CONFIG_PATH")
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: LISTENER_CONFIG_PATH environment variable is not set\n")
		os.Exit(1)
	}

	config, err := config.Read(ctx, configPath)
	if err != nil {
		log.Printf("Failed to read config: %v", err)
		os.Exit(1)
	}

	app, err := app.New(*config)
	if err != nil {
		log.Printf("Failed to initialize app: %v", err)
		os.Exit(1)
	}

	if err := app.Run(ctx); err != nil {
		log.Printf("Application returned an error: %v", err)
		os.Exit(1)
	}
}
