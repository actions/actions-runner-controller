package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/runtime"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	actionsv1alpha1 "github.com/actions-runner-controller/actions-runner-controller/api/v1alpha1"
)

func TestNewRunnerReplicaSet(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := actionsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("%v", err)
	}

	r := &RunnerDeploymentReconciler{
		CommonRunnerLabels: []string{"dev"},
		Scheme:             scheme,
	}
	rd := actionsv1alpha1.RunnerDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "example",
		},
		Spec: actionsv1alpha1.RunnerDeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"foo": "bar",
				},
			},
			Template: actionsv1alpha1.RunnerTemplate{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"foo": "bar",
					},
				},
				Spec: actionsv1alpha1.RunnerSpec{
					RunnerConfig: actionsv1alpha1.RunnerConfig{
						Labels: []string{"project1"},
					},
				},
			},
		},
	}

	rs, err := r.newRunnerReplicaSet(rd)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if val, ok := rs.Labels["foo"]; ok {
		if val != "bar" {
			t.Errorf("foo label does not have bar but %v", val)
		}
	} else {
		t.Errorf("foo label does not exist")
	}

	hash1, ok := rs.Labels[LabelKeyRunnerTemplateHash]
	if !ok {
		t.Errorf("missing runner-template-hash label")
	}

	runnerLabel := []string{"project1", "dev"}
	if d := cmp.Diff(runnerLabel, rs.Spec.Template.Spec.Labels); d != "" {
		t.Errorf("%s", d)
	}

	rd2 := rd.DeepCopy()
	rd2.Spec.Template.Spec.Labels = []string{"project2"}

	rs2, err := r.newRunnerReplicaSet(*rd2)
	if err != nil {
		t.Fatalf("%v", err)
	}

	hash2, ok := rs2.Labels[LabelKeyRunnerTemplateHash]
	if !ok {
		t.Errorf("missing runner-template-hash label")
	}

	if hash1 == hash2 {
		t.Errorf(
			"runner replica sets from runner deployments with varying labels must have different template hash, but got %s and %s",
			hash1, hash2,
		)
	}

	rd3 := rd.DeepCopy()
	rd3.Spec.Template.Labels["foo"] = "baz"

	rs3, err := r.newRunnerReplicaSet(*rd3)
	if err != nil {
		t.Fatalf("%v", err)
	}

	hash3, ok := rs3.Labels[LabelKeyRunnerTemplateHash]
	if !ok {
		t.Errorf("missing runner-template-hash label")
	}

	if hash1 == hash3 {
		t.Errorf(
			"runner replica sets from runner deployments with varying meta labels must have different template hash, but got %s and %s",
			hash1, hash3,
		)
	}
}

// SetupDeploymentTest will set up a testing environment.
// This includes:
// * creating a Namespace to be used during the test
// * starting the 'RunnerDeploymentReconciler'
// * stopping the 'RunnerDeploymentReconciler" after the test ends
// Call this function at the start of each of your tests.
func SetupDeploymentTest(ctx2 context.Context) *corev1.Namespace {
	var ctx context.Context
	var cancel func()
	ns := &corev1.Namespace{}

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(ctx2)
		*ns = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "testns-" + randStringRunes(5)},
		}

		err := k8sClient.Create(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to create test namespace")

		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Namespace: ns.Name,
		})
		Expect(err).NotTo(HaveOccurred(), "failed to create manager")

		controller := &RunnerDeploymentReconciler{
			Client:   mgr.GetClient(),
			Scheme:   scheme.Scheme,
			Log:      logf.Log,
			Recorder: mgr.GetEventRecorderFor("runnerreplicaset-controller"),
			Name:     "runnerdeployment-" + ns.Name,
		}
		err = controller.SetupWithManager(mgr)
		Expect(err).NotTo(HaveOccurred(), "failed to setup controller")

		go func() {
			defer GinkgoRecover()

			err := mgr.Start(ctx)
			Expect(err).NotTo(HaveOccurred(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		defer cancel()

		err := k8sClient.Delete(ctx, ns)
		Expect(err).NotTo(HaveOccurred(), "failed to delete test namespace")
	})

	return ns
}

var _ = Context("Inside of a new namespace", func() {
	ctx := context.TODO()
	ns := SetupDeploymentTest(ctx)

	Describe("when no existing resources exist", func() {

		It("should create a new RunnerReplicaSet resource from the specified template, add a another RunnerReplicaSet on template modification, and eventually removes old runnerreplicasets", func() {
			name := "example-runnerdeploy-1"

			{
				rs := &actionsv1alpha1.RunnerDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerDeploymentSpec{
						Replicas: intPtr(1),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"foo": "bar",
							},
						},
						Template: actionsv1alpha1.RunnerTemplate{
							ObjectMeta: metav1.ObjectMeta{
								Labels: map[string]string{
									"foo": "bar",
								},
							},
							Spec: actionsv1alpha1.RunnerSpec{
								RunnerConfig: actionsv1alpha1.RunnerConfig{
									Repository: "test/valid",
									Image:      "bar",
								},
								RunnerPodSpec: actionsv1alpha1.RunnerPodSpec{
									Env: []corev1.EnvVar{
										{Name: "FOO", Value: "FOOVALUE"},
									},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() (int, error) {
						selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
						if err != nil {
							return 0, err
						}
						err = k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
							client.MatchingLabelsSelector{Selector: selector},
						)
						if err != nil {
							return 0, err
						}
						if len(runnerSets.Items) != 1 {
							return 0, fmt.Errorf("runnerreplicasets is not 1 but %d", len(runnerSets.Items))
						}

						return *runnerSets.Items[0].Spec.Replicas, nil
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1))
			}

			{
				// We wrap the update in the Eventually block to avoid the below error that occurs due to concurrent modification
				// made by the controller to update .Status.AvailableReplicas and .Status.ReadyReplicas
				//   Operation cannot be fulfilled on runnersets.actions.summerwind.dev "example-runnerset": the object has been modified; please apply your changes to the latest version and try again
				var rd actionsv1alpha1.RunnerDeployment
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: name}, &rd)
					if err != nil {
						return fmt.Errorf("failed to get test RunnerReplicaSet resource: %v\n", err)
					}
					rd.Spec.Replicas = intPtr(2)

					return k8sClient.Update(ctx, &rd)
				},
					time.Second*1, time.Millisecond*500).Should(BeNil())

				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() (int, error) {
						selector, err := metav1.LabelSelectorAsSelector(rd.Spec.Selector)
						if err != nil {
							return 0, err
						}
						err = k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
							client.MatchingLabelsSelector{Selector: selector},
						)
						if err != nil {
							return 0, err
						}
						if len(runnerSets.Items) != 1 {
							return 0, fmt.Errorf("runnerreplicasets is not 1 but %d", len(runnerSets.Items))
						}

						return *runnerSets.Items[0].Spec.Replicas, nil
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(2))
			}
		})

		It("should create a new RunnerReplicaSet resource from the specified template without labels and selector, add a another RunnerReplicaSet on template modification, and eventually removes old runnerreplicasets", func() {
			name := "example-runnerdeploy-2"

			{
				rs := &actionsv1alpha1.RunnerDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerDeploymentSpec{
						Replicas: intPtr(1),
						Template: actionsv1alpha1.RunnerTemplate{
							Spec: actionsv1alpha1.RunnerSpec{
								RunnerConfig: actionsv1alpha1.RunnerConfig{
									Repository: "test/valid",
									Image:      "bar",
								},
								RunnerPodSpec: actionsv1alpha1.RunnerPodSpec{
									Env: []corev1.EnvVar{
										{Name: "FOO", Value: "FOOVALUE"},
									},
								},
							},
						},
					},
				}

				err := k8sClient.Create(ctx, rs)

				Expect(err).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() (int, error) {
						selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
						if err != nil {
							return 0, err
						}
						err = k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
							client.MatchingLabelsSelector{Selector: selector},
						)
						if err != nil {
							return 0, err
						}
						if len(runnerSets.Items) != 1 {
							return 0, fmt.Errorf("runnerreplicasets is not 1 but %d", len(runnerSets.Items))
						}

						return *runnerSets.Items[0].Spec.Replicas, nil
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(1))
			}

			{
				// We wrap the update in the Eventually block to avoid the below error that occurs due to concurrent modification
				// made by the controller to update .Status.AvailableReplicas and .Status.ReadyReplicas
				//   Operation cannot be fulfilled on runnersets.actions.summerwind.dev "example-runnerset": the object has been modified; please apply your changes to the latest version and try again
				var rd actionsv1alpha1.RunnerDeployment
				Eventually(func() error {
					err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns.Name, Name: name}, &rd)
					if err != nil {
						return fmt.Errorf("failed to get test RunnerReplicaSet resource: %v\n", err)
					}
					rd.Spec.Replicas = intPtr(2)

					return k8sClient.Update(ctx, &rd)
				},
					time.Second*1, time.Millisecond*500).Should(BeNil())

				runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

				Eventually(
					func() (int, error) {
						selector, err := metav1.LabelSelectorAsSelector(rd.Spec.Selector)
						if err != nil {
							return 0, err
						}
						err = k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
							client.MatchingLabelsSelector{Selector: selector},
						)
						if err != nil {
							return 0, err
						}
						if len(runnerSets.Items) != 1 {
							return 0, fmt.Errorf("runnerreplicasets is not 1 but %d", len(runnerSets.Items))
						}

						return *runnerSets.Items[0].Spec.Replicas, nil
					},
					time.Second*5, time.Millisecond*500).Should(BeEquivalentTo(2))
			}
		})

		It("should adopt RunnerReplicaSet created before 0.18.0 to have Spec.Selector", func() {
			name := "example-runnerdeploy-2"

			{
				rd := &actionsv1alpha1.RunnerDeployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: ns.Name,
					},
					Spec: actionsv1alpha1.RunnerDeploymentSpec{
						Replicas: intPtr(1),
						Template: actionsv1alpha1.RunnerTemplate{
							Spec: actionsv1alpha1.RunnerSpec{
								RunnerConfig: actionsv1alpha1.RunnerConfig{
									Repository: "test/valid",
									Image:      "bar",
								},
								RunnerPodSpec: actionsv1alpha1.RunnerPodSpec{
									Env: []corev1.EnvVar{
										{Name: "FOO", Value: "FOOVALUE"},
									},
								},
							},
						},
					},
				}

				createRDErr := k8sClient.Create(ctx, rd)
				Expect(createRDErr).NotTo(HaveOccurred(), "failed to create test RunnerReplicaSet resource")

				Eventually(
					func() (int, error) {
						runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

						err := k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
						)
						if err != nil {
							return 0, err
						}

						return len(runnerSets.Items), nil
					},
					time.Second*1, time.Millisecond*500).Should(BeEquivalentTo(1))

				var rs17 *actionsv1alpha1.RunnerReplicaSet

				Consistently(
					func() (*metav1.LabelSelector, error) {
						runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

						err := k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
						)
						if err != nil {
							return nil, err
						}
						if len(runnerSets.Items) != 1 {
							return nil, fmt.Errorf("runnerreplicasets is not 1 but %d", len(runnerSets.Items))
						}

						rs17 = &runnerSets.Items[0]

						return runnerSets.Items[0].Spec.Selector, nil
					},
					time.Second*1, time.Millisecond*500).Should(Not(BeNil()))

				// We simulate the old, pre 0.18.0 RunnerReplicaSet by updating it.
				// I've tried to use controllerutil.Set{Owner,Controller}Reference and k8sClient.Create(rs17)
				// but it didn't work due to missing RD UID, where UID is generated on K8s API server on k8sCLient.Create(rd)
				rs17.Spec.Selector = nil

				updateRSErr := k8sClient.Update(ctx, rs17)
				Expect(updateRSErr).NotTo(HaveOccurred())

				Eventually(
					func() (*metav1.LabelSelector, error) {
						runnerSets := actionsv1alpha1.RunnerReplicaSetList{Items: []actionsv1alpha1.RunnerReplicaSet{}}

						err := k8sClient.List(
							ctx,
							&runnerSets,
							client.InNamespace(ns.Name),
						)
						if err != nil {
							return nil, err
						}
						if len(runnerSets.Items) != 1 {
							return nil, fmt.Errorf("runnerreplicasets is not 1 but %d", len(runnerSets.Items))
						}

						return runnerSets.Items[0].Spec.Selector, nil
					},
					time.Second*1, time.Millisecond*500).Should(Not(BeNil()))
			}
		})

	})
})
