package actionsgithubcom

import (
	"strings"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// maxConditionMessageLength is the maximum length of metav1.Condition.Message
// enforced by the API server.
const maxConditionMessageLength = 32768

// setReadyCondition updates the Ready condition in the given condition list.
func setReadyCondition(conditions *[]metav1.Condition, generation int64, status metav1.ConditionStatus, reason, message string) {
	if len(message) > maxConditionMessageLength {
		message = strings.ToValidUTF8(message[:maxConditionMessageLength], "")
	}
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               v1alpha1.ConditionTypeReady,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
	})
}
