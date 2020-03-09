package webhook

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"testing"

	"github.com/go-logr/logr"
	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
	"github.com/summerwind/actions-runner-controller/github/fake"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	sc       *runtime.Scheme
	kClient  client.Client
	decoder  *admission.Decoder
	logger   logr.Logger
	ghClient *github.Client
	server   *httptest.Server
)

func TestMain(m *testing.M) {
	var err error

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "config", "crd", "bases")},
	}
	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}
	defer testEnv.Stop()

	sc = scheme.Scheme
	_ = corev1.AddToScheme(sc)
	_ = v1alpha1.AddToScheme(sc)

	kClient, err = client.New(cfg, client.Options{Scheme: sc})
	if err != nil {
		panic(err)
	}

	decoder, err = admission.NewDecoder(sc)
	if err != nil {
		panic(err)
	}

	logger = log.NullLogger{}

	server = fake.NewServer()
	defer server.Close()

	baseURL, err := url.Parse(server.URL + "/")
	if err != nil {
		panic(err)
	}

	ghClient = github.NewClient("token")
	ghClient.SetBaseURL(baseURL)

	m.Run()
}

func TestHandle(t *testing.T) {
	validRunner := v1alpha1.Runner{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-valid",
		},
		Spec: v1alpha1.RunnerSpec{
			Repository: "test/valid",
		},
	}

	if err := kClient.Create(context.Background(), &validRunner); err != nil {
		t.Fatalf("failed to create runner")
	}
	defer kClient.Delete(context.Background(), &validRunner)

	errorRunner := v1alpha1.Runner{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-error",
		},
		Spec: v1alpha1.RunnerSpec{
			Repository: "test/error",
		},
	}

	if err := kClient.Create(context.Background(), &errorRunner); err != nil {
		t.Fatalf("failed to create runner")
	}
	defer kClient.Delete(context.Background(), &errorRunner)

	injector := RegistrationTokenInjector{
		Client:       kClient,
		Log:          logger,
		GitHubClient: ghClient,
		decoder:      decoder,
	}

	runnerPatches := []jsonpatch.JsonPatchOperation{
		{
			Operation: "add",
			Path:      "/spec/containers/0/env",
			Value: []map[string]string{
				{
					"name":  "RUNNER_REPO",
					"value": "test/valid",
				},
				{
					"name":  "RUNNER_TOKEN",
					"value": "fake-registration-token",
				},
			},
		},
		{
			Operation: "replace",
			Path:      "/spec/restartPolicy",
			Value:     "OnFailure",
		},
	}

	emptyPatches := []jsonpatch.JsonPatchOperation{}

	tests := []struct {
		runnerName string
		runnerRepo string
		patches    []jsonpatch.JsonPatchOperation
		err        bool
	}{
		{"", "", emptyPatches, false},
		{"test-valid", "test/valid", runnerPatches, false},
		{"test-valid", "test/invalid", emptyPatches, false},
		{"test-notfound", "test/valid", emptyPatches, false},
		{"test-error", "test/error", nil, true},
	}

	for i, tt := range tests {
		pod := corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   "default",
				Name:        "test",
				Labels:      map[string]string{},
				Annotations: map[string]string{},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					corev1.Container{
						Name:  v1alpha1.ContainerName,
						Image: "runner:latest",
					},
				},
				RestartPolicy: corev1.RestartPolicy("Always"),
			},
		}

		if tt.runnerName != "" {
			pod.ObjectMeta.Labels[v1alpha1.KeyRunnerName] = tt.runnerName
		}
		if tt.runnerRepo != "" {
			pod.ObjectMeta.Annotations[v1alpha1.KeyRunnerRepository] = tt.runnerRepo
		}

		buf, err := json.Marshal(&pod)
		if err != nil {
			t.Fatalf("[%d] failed to marshal pod resource: %s", i, err)
		}

		req := admission.Request{
			AdmissionRequest: admissionv1beta1.AdmissionRequest{
				Object: runtime.RawExtension{
					Raw: buf,
				},
			},
		}

		res := injector.Handle(context.Background(), req)
		if tt.err {
			if res.Allowed {
				t.Errorf("[%d] unexpected response: %v", i, res)
			}
		} else {
			sort.Slice(res.Patches, func(i, j int) bool {
				if res.Patches[i].Operation == res.Patches[j].Operation {
					if len(res.Patches[i].Path) < len(res.Patches[i].Path) {
						return true
					}
				} else if res.Patches[i].Operation == "add" {
					return true
				}

				return false
			})

			ttBuf, err := json.Marshal(tt.patches)
			if err != nil {
				t.Fatalf("[%d] failed to marshal JSON patch: %s", i, err)
			}
			resBuf, err := json.Marshal(res.Patches)
			if err != nil {
				t.Fatalf("[%d] failed to marshal JSON patch: %s", i, err)
			}

			if string(ttBuf) != string(resBuf) {
				t.Errorf("[%d] unexpected patches - got: %v, want: %v", i, string(resBuf), string(ttBuf))
			}
		}
	}
}
