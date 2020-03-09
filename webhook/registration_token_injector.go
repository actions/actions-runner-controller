package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-logr/logr"
	"gomodules.xyz/jsonpatch/v2"
	admissionv1beta1 "k8s.io/api/admission/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,groups="",resources=pods,verbs=create,versions=v1,name=runner-pod.webhook.actions.summerwind.dev

type RegistrationTokenInjector struct {
	client.Client
	Log          logr.Logger
	GitHubClient *github.Client
	decoder      *admission.Decoder
}

func (t *RegistrationTokenInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	var pod corev1.Pod
	err := t.decoder.Decode(req, &pod)
	if err != nil {
		t.Log.Error(err, "Failed to decode request object")
		return admission.Errored(http.StatusBadRequest, err)
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	runnerName, okName := pod.Labels[v1alpha1.KeyRunnerName]
	repo, okRepo := pod.Annotations[v1alpha1.KeyRunnerRepository]
	if !okName || !okRepo {
		return newEmptyResponse()
	}

	nn := types.NamespacedName{
		Namespace: pod.Namespace,
		Name:      runnerName,
	}

	var runner v1alpha1.Runner
	if err := t.Get(ctx, nn, &runner); err != nil {
		if errors.IsNotFound(err) {
			return newEmptyResponse()
		}

		t.Log.Error(err, "Failed to get Runner")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	if runner.Spec.Repository != repo {
		return newEmptyResponse()
	}

	rt, err := t.GitHubClient.GetRegistrationToken(context.Background(), repo, pod.Name)
	if err != nil {
		t.Log.Error(err, "Failed to get new registration token")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	for i, c := range pod.Spec.Containers {
		if c.Name == v1alpha1.ContainerName {
			env := []corev1.EnvVar{
				{
					Name:  v1alpha1.EnvRunnerRepository,
					Value: repo,
				},
				{
					Name:  v1alpha1.EnvRunnerToken,
					Value: rt,
				},
			}
			pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, env...)
		}
	}

	if pod.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		pod.Spec.RestartPolicy = corev1.RestartPolicyOnFailure
	}

	buf, err := json.Marshal(pod)
	if err != nil {
		t.Log.Error(err, "Failed to encode new object")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	res := admission.PatchResponseFromRaw(req.Object.Raw, buf)
	return res
}

func (t *RegistrationTokenInjector) InjectDecoder(d *admission.Decoder) error {
	t.decoder = d
	return nil
}

func newEmptyResponse() admission.Response {
	pt := admissionv1beta1.PatchTypeJSONPatch
	return admission.Response{
		Patches: []jsonpatch.Operation{},
		AdmissionResponse: admissionv1beta1.AdmissionResponse{
			Allowed:   true,
			PatchType: &pt,
		},
	}
}
