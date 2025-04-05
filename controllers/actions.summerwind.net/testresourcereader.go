package actionssummerwindnet

import (
	"context"
	"errors"
	"reflect"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type testResourceReader struct {
	objects map[types.NamespacedName]client.Object
}

func (r *testResourceReader) Get(_ context.Context, key client.ObjectKey, obj client.Object, _ ...client.GetOption) error {
	nsName := types.NamespacedName(key)
	ret, ok := r.objects[nsName]
	if !ok {
		return &kerrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}}
	}
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Ptr {
		return errors.New("obj must be a pointer")
	}

	v.Elem().Set(reflect.ValueOf(ret).Elem())

	return nil
}
