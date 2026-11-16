package actionsgithubcom

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var _ = Describe("Runner cache leader handoff", func() {
	It("uses the standby's warm cache to finish an interrupted batch under a shared lease", func() {
		ctx := context.Background()
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "runner-cache-"}}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func() { Expect(k8sClient.Delete(ctx, ns)).To(Succeed()) })

		newManager := func() (ctrl.Manager, *EphemeralRunnerSetReconciler) {
			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme:     k8sClient.Scheme(),
				Controller: config.Controller{SkipNameValidation: ptr.To(true)},
				Cache: cache.Options{DefaultNamespaces: map[string]cache.Config{
					ns.Name: {},
				}},
				LeaderElection:                true,
				LeaderElectionID:              "runner-cache",
				LeaderElectionNamespace:       ns.Name,
				LeaderElectionReleaseOnCancel: true,
				LeaseDuration:                 ptr.To(3 * time.Second),
				RenewDeadline:                 ptr.To(2 * time.Second),
				RetryPeriod:                   ptr.To(200 * time.Millisecond),
				Metrics:                       metricsserver.Options{BindAddress: "0"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(SetupIndexers(mgr)).To(Succeed())
			r := &EphemeralRunnerSetReconciler{
				Client: mgr.GetClient(), Scheme: mgr.GetScheme(), Log: logr.Discard(),
				ResourceBuilder: ResourceBuilder{Scheme: mgr.GetScheme(), ResourceCache: newTestResourceCache()},
			}
			Expect(mgr.Add(manager.RunnableFunc(func(ctx context.Context) error {
				<-ctx.Done()
				r.ClearRunnerCreationCache()
				return nil
			}))).To(Succeed())
			return mgr, r
		}
		first, firstController := newManager()
		second, secondController := newManager()
		var attempts atomic.Int32
		created := make(chan struct{})
		firstController.Client = &runnerCacheCreateClient{
			Client: first.GetClient(),
			create: func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*v1alpha1.EphemeralRunner); !ok {
					return k8sClient.Create(ctx, obj, opts...)
				}
				if attempts.Add(1) != 1 {
					<-ctx.Done()
					return ctx.Err()
				}
				if err := k8sClient.Create(ctx, obj, opts...); err != nil {
					return err
				}
				close(created)
				return nil
			},
		}
		Expect(firstController.SetupWithManager(first)).To(Succeed())
		Expect(secondController.SetupWithManager(second)).To(Succeed())
		start := func(mgr ctrl.Manager) context.CancelFunc {
			managerCtx, cancel := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() { done <- mgr.Start(managerCtx) }()
			DeferCleanup(func() {
				cancel()
				Eventually(done, 10*time.Second).Should(Receive(BeNil()))
			})
			return cancel
		}
		stopFirst := start(first)
		Eventually(first.Elected(), 10*time.Second).Should(BeClosed())
		start(second)
		set := &v1alpha1.EphemeralRunnerSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "handoff", Namespace: ns.Name,
				Finalizers: []string{EphemeralRunnerSetFinalizerName},
			},
			Spec: v1alpha1.EphemeralRunnerSetSpec{
				Replicas: 3,
				EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{
					GitHubConfigURL: "https://github.com/org/repo",
					PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "runner", Image: "runner:latest"}},
					}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, set)).To(Succeed())
		Eventually(created, 10*time.Second).Should(BeClosed())
		Eventually(func(g Gomega) {
			var cached v1alpha1.EphemeralRunnerList
			g.Expect(second.GetClient().List(ctx, &cached, client.InNamespace(ns.Name))).To(Succeed())
			g.Expect(cached.Items).To(HaveLen(1), "standby already watches the previous leader's writes")
			g.Expect(second.GetClient().Get(ctx, client.ObjectKeyFromObject(set), new(v1alpha1.EphemeralRunnerSet))).To(Succeed())
		}, 10*time.Second).Should(Succeed())
		Eventually(func() bool {
			_, found := firstController.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
			return found
		}, 10*time.Second).Should(BeTrue())
		stopFirst()
		Eventually(func() bool {
			_, found := firstController.runnerCreationCache.sets.Load(client.ObjectKeyFromObject(set))
			return !found
		}, 10*time.Second).Should(BeTrue(), "the previous leader drops its local inventory")
		Eventually(second.Elected(), 15*time.Second).Should(BeClosed())
		Eventually(func(g Gomega) {
			var runners v1alpha1.EphemeralRunnerList
			g.Expect(k8sClient.List(ctx, &runners, client.InNamespace(ns.Name))).To(Succeed())
			g.Expect(runners.Items).To(HaveLen(3))
		}, 15*time.Second).Should(Succeed())
		Consistently(func() int {
			var runners v1alpha1.EphemeralRunnerList
			Expect(k8sClient.List(ctx, &runners, client.InNamespace(ns.Name))).To(Succeed())
			return len(runners.Items)
		}, time.Second, 50*time.Millisecond).Should(Equal(3))
	})
})

type runnerCacheCreateClient struct {
	client.Client
	create func(context.Context, client.Object, ...client.CreateOption) error
}

func (c *runnerCacheCreateClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return c.create(ctx, obj, opts...)
}
