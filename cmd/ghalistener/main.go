package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/app"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
)

func main() {
	config, err := config.Read()
	if err != nil {
		log.Printf("Failed to read config: %v", err)
		os.Exit(1)
	}

	app, err := app.New(config)
	if err != nil {
		log.Printf("Failed to initialize app: %v", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		log.Printf("Application returned an error: %v", err)
		os.Exit(1)
	}
}
