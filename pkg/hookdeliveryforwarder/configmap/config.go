package configmap

import (
	"flag"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

type Config struct {
	Name      string
	Namespace string
	Logger    logr.Logger
	Scheme    *runtime.Scheme
}

func (c *Config) InitFlags(fs *flag.FlagSet) {
	fs.StringVar(&c.Name, "configmap-name", "gh-webhook-forwarder", `The name of the Kubernetes ConfigMap to which store state for check-pointing.`)
	fs.StringVar(&c.Namespace, "namespace", "default", `The Kubernetes namespace to store configmap for check-pointing.`)
}

func New(checkpointerConfig *Config) (*ConfigMapCheckpointer, manager.Manager, error) {
	ctrl.SetLogger(checkpointerConfig.Logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:           checkpointerConfig.Scheme,
		LeaderElectionID: "hookdeliveryforwarder",
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 9443,
		}),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("unable to start manager: %v", err)
	}

	return &ConfigMapCheckpointer{
		Client: mgr.GetClient(),
		Name:   checkpointerConfig.Name,
		NS:     checkpointerConfig.Namespace,
	}, mgr, nil
}
