package main

import (
	"log"
	"os"

	"github.com/actions/actions-runner-controller/cmd/ghalistener/app"
	"github.com/actions/actions-runner-controller/cmd/ghalistener/config"
)

func main() {
	config, err := config.Read()
	if err != nil {
		log.Printf("failed to read config: %v", err)
		os.Exit(1)
	}

	app, err := app.New(config)
	if err != nil {
		log.Printf("failed to initialize app: %v", err)
		os.Exit(1)
	}
}
