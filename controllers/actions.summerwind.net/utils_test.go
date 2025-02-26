package actionssummerwindnet

import (
	"reflect"
	"testing"

	"github.com/actions/actions-runner-controller/apis/actions.summerwind.net/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func Test_filterLabels(t *testing.T) {
	type args struct {
		labels map[string]string
		filter string
	}
	tests := []struct {
		name string
		args args
		want map[string]string
	}{
		{
			name: "ok",
			args: args{
				labels: map[string]string{LabelKeyRunnerTemplateHash: "abc", LabelKeyPodTemplateHash: "def"},
				filter: LabelKeyRunnerTemplateHash,
			},
			want: map[string]string{LabelKeyPodTemplateHash: "def"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := filterLabels(tt.args.labels, tt.args.filter); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_workVolumeClaimTemplateVolumeV1VolumeTransformation(t *testing.T) {
	storageClassName := "local-storage"
	workVolumeClaimTemplate := v1alpha1.WorkVolumeClaimTemplate{
		StorageClassName: storageClassName,
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, corev1.ReadWriteMany},
		Resources:        corev1.VolumeResourceRequirements{},
	}
	want := corev1.Volume{
		Name: "work",
		VolumeSource: corev1.VolumeSource{
			Ephemeral: &corev1.EphemeralVolumeSource{
				VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, corev1.ReadWriteMany},
						StorageClassName: &storageClassName,
						Resources:        corev1.VolumeResourceRequirements{},
					},
				},
			},
		},
	}

	got := workVolumeClaimTemplate.V1Volume()

	if got.Name != want.Name {
		t.Errorf("want name %q, got %q\n", want.Name, got.Name)
	}

	if got.VolumeSource.Ephemeral == nil {
		t.Fatal("work volume claim template should transform itself into Ephemeral volume source\n")
	}

	if got.VolumeSource.Ephemeral.VolumeClaimTemplate == nil {
		t.Fatal("work volume claim template should have ephemeral volume claim template set\n")
	}

	gotClassName := *got.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.StorageClassName
	wantClassName := *want.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.StorageClassName
	if gotClassName != wantClassName {
		t.Errorf("expected storage class name %q, got %q\n", wantClassName, gotClassName)
	}

	gotAccessModes := got.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.AccessModes
	wantAccessModes := want.VolumeSource.Ephemeral.VolumeClaimTemplate.Spec.AccessModes
	if len(gotAccessModes) != len(wantAccessModes) {
		t.Fatalf("access modes lengths missmatch: got %v, expected %v\n", gotAccessModes, wantAccessModes)
	}

	diff := make(map[corev1.PersistentVolumeAccessMode]int, len(wantAccessModes))
	for _, am := range wantAccessModes {
		diff[am]++
	}

	for _, am := range gotAccessModes {
		_, ok := diff[am]
		if !ok {
			t.Errorf("got access mode %v that is not in the wanted access modes\n", am)
		}

		diff[am]--
		if diff[am] == 0 {
			delete(diff, am)
		}
	}

	if len(diff) != 0 {
		t.Fatalf("got access modes did not take every access mode into account\nactual: %v expected: %v\n", gotAccessModes, wantAccessModes)
	}
}

func Test_workVolumeClaimTemplateV1VolumeMount(t *testing.T) {
	workVolumeClaimTemplate := v1alpha1.WorkVolumeClaimTemplate{
		StorageClassName: "local-storage",
		AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, corev1.ReadWriteMany},
		Resources:        corev1.VolumeResourceRequirements{},
	}

	mountPath := "/test/_work"
	want := corev1.VolumeMount{
		MountPath: mountPath,
		Name:      "work",
	}

	got := workVolumeClaimTemplate.V1VolumeMount(mountPath)

	if want != got {
		t.Fatalf("expected volume mount %+v, actual %+v\n", want, got)
	}
}
