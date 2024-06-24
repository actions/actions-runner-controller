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

	"go.opentelemetry.io/otel"
	ddotel "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/opentelemetry"

	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	"go.opentelemetry.io/otel/log/global"
	otellog "go.opentelemetry.io/otel/sdk/log"
)

func main() {
	provider := ddotel.NewTracerProvider()
	defer provider.Shutdown()

	otel.SetTracerProvider(provider)

	loggerProvider, err := newLoggerProvider()
	if err != nil {
		return
	}

	global.SetLoggerProvider(loggerProvider)

	log.Printf("Enabled OpenTelemetry Tracing")

	configPath, ok := os.LookupEnv("LISTENER_CONFIG_PATH")
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: LISTENER_CONFIG_PATH environment variable is not set\n")
		os.Exit(1)
	}
	config, err := config.Read(configPath)
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

func newLoggerProvider() (*otellog.LoggerProvider, error) {
	logExporter, err := stdoutlog.New()
	if err != nil {
		return nil, err
	}

	loggerProvider := otellog.NewLoggerProvider(
		otellog.WithProcessor(otellog.NewBatchProcessor(logExporter)),
	)
	return loggerProvider, nil
}
