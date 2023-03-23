package actionsgithubcom

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"

	actionsv1alpha1 "github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
)

func TestMain(m *testing.M) {
	logf.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(io.Discard)))

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("../..", "config", "crd", "bases")},
	}

	// Avoids the following error:
	// 2021-03-19T15:14:11.673+0900    ERROR   controller-runtime.controller
	// Reconciler error      {"controller": "testns-tvjzjrunner", "request":
	// "testns-gdnyx/example-runnerdeploy-zps4z-j5562", "error": "Pod
	// \"example-runnerdeploy-zps4z-j5562\" is invalid:
	// [spec.containers[1].image: Required value,
	// spec.containers[1].securityContext.privileged: Forbidden: disallowed by
	// cluster policy]"}
	testEnv.ControlPlane.GetAPIServer().Configure().
		Append("allow-privileged", "true")

	var err error
	cfg, err = testEnv.Start()
	if err != nil || cfg == nil {
		log.Fatalln(err, "failed to start test environment")
	}

	err = actionsv1alpha1.AddToScheme(scheme.Scheme)
	if err != nil {
		log.Fatalln(err, "failed to add scheme")
	}

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil || k8sClient == nil {
		log.Fatalln(err, "failed to create client")
	}

	// run the tests
	code := m.Run()

	err = testEnv.Stop()
	if err != nil {
		log.Fatalln(err, "failed to stop test environment")
	}

	os.Exit(code)
}
