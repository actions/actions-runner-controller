package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/actions-runner-controller/actions-runner-controller/pkg/githubwebhookdeliveryforwarder"
	zaplib "go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type logPositionProvider struct {
	name   string
	ns     string
	client client.Client
}

type posData struct {
	DeliveredAt time.Time `json:"delivered_at"`
	ID          int64     `json:"id"`
}

func (p *logPositionProvider) GetOrCreate(hookID int64) (*githubwebhookdeliveryforwarder.DeliveryLogPosition, error) {
	var cm corev1.ConfigMap

	if err := p.client.Get(context.Background(), types.NamespacedName{Namespace: p.ns, Name: p.name}, &cm); err != nil {
		if !kerrors.IsNotFound(err) {
			return nil, err
		}

		cm.Name = p.name
		cm.Namespace = p.ns

		if err := p.client.Create(context.Background(), &cm); err != nil {
			return nil, err
		}
	}

	idStr := fmt.Sprintf("hook_%d", hookID)

	var unmarshalled posData

	data, ok := cm.Data[idStr]

	if ok {
		if err := json.Unmarshal([]byte(data), &unmarshalled); err != nil {
			return nil, err
		}
	}

	pos := &githubwebhookdeliveryforwarder.DeliveryLogPosition{
		DeliveredAt: unmarshalled.DeliveredAt,
		ID:          unmarshalled.ID,
	}

	if pos.DeliveredAt.IsZero() {
		pos.DeliveredAt = time.Now()
	}

	return pos, nil
}

func (p *logPositionProvider) Update(hookID int64, pos *githubwebhookdeliveryforwarder.DeliveryLogPosition) error {
	var cm corev1.ConfigMap

	if err := p.client.Get(context.Background(), types.NamespacedName{Namespace: p.ns, Name: p.name}, &cm); err != nil {
		return err
	}

	var posData posData

	posData.DeliveredAt = pos.DeliveredAt
	posData.ID = pos.ID

	idStr := fmt.Sprintf("hook_%d", hookID)

	data, err := json.Marshal(posData)
	if err != nil {
		return err
	}

	copy := cm.DeepCopy()

	if copy.Data == nil {
		copy.Data = map[string]string{}
	}

	copy.Data[idStr] = string(data)

	if err := p.client.Patch(context.Background(), copy, client.MergeFrom(&cm)); err != nil {
		return err
	}

	return nil
}

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
		logLevel  string
		namespace string
	)

	flag.StringVar(&namespace, "namespace", "default", `The Kubernetes namespace to store configmap for check-pointing.`)
	flag.StringVar(&logLevel, "log-level", logLevelDebug, `The verbosity of the logging. Valid values are "debug", "info", "warn", "error". Defaults to "debug".`)

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

	ctrl.SetLogger(logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           scheme,
		LeaderElectionID: "githubwebhookdeliveryforwarder",
		Port:             9443,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to start manager: %v\n", err)
		os.Exit(1)
	}

	config := &githubwebhookdeliveryforwarder.Config{}

	config.InitFlags((flag.CommandLine))

	flag.Parse()

	var p githubwebhookdeliveryforwarder.LogPositionProvider = &logPositionProvider{
		client: mgr.GetClient(),
		name:   "gh-webhook-forwarder",
		ns:     namespace,
	}

	// TODO: Set to something that is backed by a CRD so that
	// restarting the forwarder doesn't result in missing deliveries.
	config.LogPositionProvider = p

	ctx := githubwebhookdeliveryforwarder.SetupSignalHandler()

	go func() {
		if err := mgr.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "problem running manager: %v\n", err)
			os.Exit(1)
		}
	}()

	githubwebhookdeliveryforwarder.Run(ctx, config)
}
