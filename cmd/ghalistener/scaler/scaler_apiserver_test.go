package scaler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/actions/scaleset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestHandleJobStartedAgainstAPIServer exercises HandleJobStarted against a real
// API server. The unit tests above emulate the optimistic concurrency check that
// kube-apiserver performs when a merge patch carries metadata.resourceVersion;
// this test pins that emulation to the real behaviour.
//
// The race is made deterministic by writing the terminal phase from inside the
// client transport, right before the scaler's patch reaches the API server.
func TestHandleJobStartedAgainstAPIServer(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set; run via `make test`")
	}

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, env.Stop())
	})

	sch := runtime.NewScheme()
	require.NoError(t, scheme.AddToScheme(sch))
	require.NoError(t, v1alpha1.AddToScheme(sch))
	k8sClient, err := client.New(cfg, client.Options{Scheme: sch})
	require.NoError(t, err)

	ctx := context.Background()

	jobInfo := &scaleset.JobStarted{
		RunnerName: "runner-1",
		JobMessageBase: scaleset.JobMessageBase{
			OwnerName:       "actions",
			RepositoryName:  "actions-runner-controller",
			JobID:           "job-1",
			WorkflowRunID:   456,
			JobWorkflowRef:  "actions/actions-runner-controller/.github/workflows/ci.yaml@refs/heads/main",
			JobDisplayName:  "build",
			RunnerRequestID: 123,
		},
	}

	newRunner := func(t *testing.T, name string) *v1alpha1.EphemeralRunner {
		t.Helper()

		runner := &v1alpha1.EphemeralRunner{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec: v1alpha1.EphemeralRunnerSpec{
				GitHubConfigURL:    "https://github.com/actions",
				GitHubConfigSecret: "secret",
				RunnerScaleSetID:   1,
				PodTemplateSpec: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}},
					},
				},
			},
		}
		require.NoError(t, k8sClient.Create(ctx, runner))
		return runner
	}

	newScaler := func(t *testing.T, beforePatch func()) *Scaler {
		t.Helper()

		conf := rest.CopyConfig(cfg)
		if beforePatch != nil {
			var once sync.Once
			conf.Wrap(func(rt http.RoundTripper) http.RoundTripper {
				return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodPatch {
						once.Do(beforePatch)
					}
					return rt.RoundTrip(req)
				})
			})
		}

		clientset, err := kubernetes.NewForConfig(conf)
		require.NoError(t, err)

		return &Scaler{
			clientset:     clientset,
			config:        Config{EphemeralRunnerSetNamespace: "default"},
			targetRunners: -1,
			patchSeq:      -1,
			logger:        discardLogger,
		}
	}

	t.Run("transitions an idle runner to Running", func(t *testing.T) {
		runner := newRunner(t, "runner-running")
		jobInfo := *jobInfo
		jobInfo.RunnerName = runner.Name

		require.NoError(t, newScaler(t, nil).HandleJobStarted(ctx, &jobInfo))

		require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(runner), runner))
		assert.Equal(t, v1alpha1.EphemeralRunnerPhaseRunning, runner.Status.Phase)
		assert.Equal(t, jobInfo.JobID, runner.Status.JobID)
	})

	t.Run("does not resurrect a runner that failed after the read", func(t *testing.T) {
		runner := newRunner(t, "runner-raced")
		jobInfo := *jobInfo
		jobInfo.RunnerName = runner.Name

		scaler := newScaler(t, func() {
			failed := runner.DeepCopy()
			failed.Status.Phase = v1alpha1.EphemeralRunnerPhaseFailed
			require.NoError(t, k8sClient.Status().Patch(ctx, failed, client.MergeFrom(runner)))
		})

		require.NoError(t, scaler.HandleJobStarted(ctx, &jobInfo))

		require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(runner), runner))
		assert.Equal(t, v1alpha1.EphemeralRunnerPhaseFailed, runner.Status.Phase)
		assert.Equal(t, jobInfo.JobID, runner.Status.JobID, "job details are still recorded")
	})

	t.Run("ignores a runner that no longer exists", func(t *testing.T) {
		jobInfo := *jobInfo
		jobInfo.RunnerName = "runner-missing"

		assert.NoError(t, newScaler(t, nil).HandleJobStarted(ctx, &jobInfo))
	})
}
