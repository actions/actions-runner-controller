package webhook

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/summerwind/actions-runner-controller/api/v1alpha1"
	"github.com/summerwind/actions-runner-controller/github"
)

// +kubebuilder:webhook:path=/mutate-v1-pod,mutating=true,failurePolicy=ignore,groups="",resources=pods,verbs=create,versions=v1,name=runner-pod.webhook.actions.summerwind.dev

type RegistrationTokenInjector struct {
	GitHubClient *github.Client
	Log          logr.Logger
	decoder      *admission.Decoder
}

func (t *RegistrationTokenInjector) Handle(ctx context.Context, req admission.Request) admission.Response {
	pod := &corev1.Pod{}
	err := t.decoder.Decode(req, pod)
	if err != nil {
		t.Log.Error(err, "Failed to decode request object")
		return admission.Errored(http.StatusBadRequest, err)
	}

	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}

	repo, ok := pod.Annotations[v1alpha1.KeyRunnerRepository]
	if ok {
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
