package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestResourceReader(t *testing.T) {
	rr := &testResourceReader{
		objects: map[types.NamespacedName]client.Object{
			{Namespace: "default", Name: "sec1"}: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "sec1",
				},
				Data: map[string][]byte{
					"foo": []byte("bar"),
				},
			},
		},
	}

	var sec corev1.Secret

	err := rr.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "sec1"}, &sec)
	require.NoError(t, err)

	require.Equal(t, []byte("bar"), sec.Data["foo"])
}
