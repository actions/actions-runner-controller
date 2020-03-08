package webhook

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	scheme   *runtime.Scheme
	decoder  *admission.Decoder
	logger   logr.Logger
	ghClient *github.Client
	server   *httptest.Server
)

func TestMain(m *testing.M) {
	var err error

	scheme = runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	decoder, err = admission.NewDecoder(scheme)
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
	injector := RegistrationTokenInjector{
		Log:          logger,
		GitHubClient: ghClient,
		decoder:      decoder,
	}

	runnerPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "runner",
			Annotations: map[string]string{
				v1alpha1.KeyRunnerRepository: "test/ok",
			},
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

	runnerErrorPod := runnerPod.DeepCopy()
	runnerErrorPod.ObjectMeta.Annotations[v1alpha1.KeyRunnerRepository] = "test/error"

	normalPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "normal",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				corev1.Container{
					Name:  "nginx",
					Image: "nginx:latest",
				},
			},
			RestartPolicy: corev1.RestartPolicy("Always"),
		},
	}

	runnerPodPatches := []jsonpatch.JsonPatchOperation{
		{
			Operation: "add",
			Path:      "/spec/containers/0/env",
			Value: []map[string]string{
				{
					"name":  "RUNNER_REPO",
					"value": "test/ok",
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

	normalPodPatches := []jsonpatch.JsonPatchOperation{}

	tests := []struct {
		pod     *corev1.Pod
		patches []jsonpatch.JsonPatchOperation
		err     bool
	}{
		{runnerPod, runnerPodPatches, false},
		{normalPod, normalPodPatches, false},
		{runnerErrorPod, nil, true},
	}

	for _, tt := range tests {
		buf, err := json.Marshal(tt.pod)
		if err != nil {
			t.Fatalf("failed to marshal pod resource: %s", err)
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
				t.Errorf("unexpected response: %v", res)
			}
		} else {
			ttBuf, err := json.Marshal(tt.patches)
			if err != nil {
				t.Fatalf("failed to marshal JSON patch: %s", err)
			}
			resBuf, err := json.Marshal(res.Patches)
			if err != nil {
				t.Fatalf("failed to marshal JSON patch: %s", err)
			}

			if string(ttBuf) != string(resBuf) {
				t.Errorf("unexpected patches - got: %v, want: %v", string(ttBuf), string(resBuf))
			}
		}
	}
}
