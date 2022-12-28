package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/actions/actions-runner-controller/pkg/hookdeliveryforwarder"
	"github.com/actions/actions-runner-controller/pkg/hookdeliveryforwarder/configmap"
	"github.com/go-logr/logr"
	zaplib "go.uber.org/zap"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"

	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	logLevelDebug = "debug"
	logLevelInfo  = "info"
	logLevelWarn  = "warn"
	logLevelError = "error"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
}

func main() {
	var (
		logLevel string

		checkpointerConfig configmap.Config
	)

	flag.StringVar(&logLevel, "log-level", logLevelDebug, `The verbosity of the logging. Valid values are "debug", "info", "warn", "error". Defaults to "debug".`)

	checkpointerConfig.InitFlags(flag.CommandLine)

	config := &hookdeliveryforwarder.Config{}

	config.InitFlags((flag.CommandLine))

	flag.Parse()

	logger := newZapLogger(logLevel)

	checkpointerConfig.Scheme = scheme
	checkpointerConfig.Logger = logger

	p, mgr, err := configmap.New(&checkpointerConfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// TODO: Set to something that is backed by a CRD so that
	// restarting the forwarder doesn't result in missing deliveries.
	config.Checkpointer = p

	ctx := hookdeliveryforwarder.SetupSignalHandler()

	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "problem running manager: %v\n", err)
			os.Exit(1)
		}
	}()

	hookdeliveryforwarder.Run(ctx, config)
}

func newZapLogger(logLevel string) logr.Logger {
	return zap.New(func(o *zap.Options) {
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
}
