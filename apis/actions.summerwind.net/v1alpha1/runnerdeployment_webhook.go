/*
Copyright 2020 The actions-runner-controller authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// log is for logging in this package.
var runnerDeploymentLog = logf.Log.WithName("runnerdeployment-resource")

func (r *RunnerDeployment) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-actions-summerwind-dev-v1alpha1-runnerdeployment,verbs=create;update,mutating=true,failurePolicy=fail,groups=actions.summerwind.dev,resources=runnerdeployments,versions=v1alpha1,name=mutate.runnerdeployment.actions.summerwind.dev,sideEffects=None,admissionReviewVersions=v1beta1

var _ webhook.CustomDefaulter = &RunnerDeploymentDefaulter{}

type RunnerDeploymentDefaulter struct{}

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (*RunnerDeploymentDefaulter) Default(context.Context, runtime.Object) error {
	// Nothing to do.
	return nil
}

// +kubebuilder:webhook:path=/validate-actions-summerwind-dev-v1alpha1-runnerdeployment,verbs=create;update,mutating=false,failurePolicy=fail,groups=actions.summerwind.dev,resources=runnerdeployments,versions=v1alpha1,name=validate.runnerdeployment.actions.summerwind.dev,sideEffects=None,admissionReviewVersions=v1beta1

var _ webhook.CustomValidator = &RunnerDeploymentValidator{}

type RunnerDeploymentValidator struct{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (*RunnerDeploymentValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*RunnerDeployment)
	if !ok {
		return nil, fmt.Errorf("expected RunnerDeployment object, got %T", obj)
	}
	runnerDeploymentLog.Info("validate resource to be created", "name", r.Name)
	return nil, r.Validate()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (*RunnerDeploymentValidator) ValidateUpdate(ctx context.Context, old, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*RunnerDeployment)
	if !ok {
		return nil, fmt.Errorf("expected RunnerDeployment object, got %T", obj)
	}
	runnerDeploymentLog.Info("validate resource to be updated", "name", r.Name)
	return nil, r.Validate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (*RunnerDeploymentValidator) ValidateDelete(context.Context, runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// Validate validates resource spec.
func (r *RunnerDeployment) Validate() error {
	errList := r.Spec.Template.Spec.Validate(field.NewPath("spec", "template", "spec"))

	if len(errList) > 0 {
		return apierrors.NewInvalid(r.GroupVersionKind().GroupKind(), r.Name, errList)
	}

	return nil
}
