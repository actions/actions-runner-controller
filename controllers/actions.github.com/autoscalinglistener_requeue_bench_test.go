package actionsgithubcom

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
)

// TestListenerRequeueBench drives the real AutoscalingListener controller
// against envtest and measures, per lifecycle phase, how long the listener
// takes to converge and how many reconciles it spends doing it.
//
// It is opt-in: ARC_LISTENER_BENCH=1.
//
// envtest has no kubelet, so a pod with no node is deleted immediately. To
// model a listener pod running out its termination grace period, the bench
// holds pods with a finalizer and releases it after a fixed hold.
func TestListenerRequeueBench(t *testing.T) {
	if os.Getenv("ARC_LISTENER_BENCH") == "" {
		t.Skip("set ARC_LISTENER_BENCH=1 to run")
	}
	logf.SetLogger(logr.Discard())

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("../..", "config", "crd", "bases")},
	}
	restCfg, err := env.Start()
	require.NoError(t, err)
	t.Cleanup(func() { _ = env.Stop() })
	require.NoError(t, v1alpha1.AddToScheme(scheme.Scheme))

	// The observer must not be what limits the measurement: no client-side
	// rate limiting, and one List per poll rather than a Get per listener.
	adminCfg := rest.CopyConfig(restCfg)
	adminCfg.QPS = -1
	admin, err := client.New(adminCfg, client.Options{Scheme: scheme.Scheme})
	require.NoError(t, err)

	variant := os.Getenv("ARC_LISTENER_BENCH_VARIANT")
	reps := benchEnvInt("ARC_LISTENER_BENCH_REPS", 5)
	large := benchEnvInt("ARC_LISTENER_BENCH_N", 25)

	for _, n := range []int{1, large} {
		agg := newBenchAggregate()
		runs := reps
		if n > 1 {
			runs = 1
		}
		for range runs {
			runListenerBench(t, restCfg, admin, n, agg)
		}
		agg.print(t, variant, n)
	}
}

const (
	benchHold             = 5 * time.Second
	benchSecretHold       = 2 * time.Second
	benchSettle           = 2500 * time.Millisecond
	benchTimeout          = 60 * time.Second
	benchPoll             = 2 * time.Millisecond
	benchPodHoldFinalizer = "bench.actions.github.com/hold"
)

// ---------------------------------------------------------------------------
// Recording client: attributes every reconcile, and every write it makes, by
// the reconcile ID controller-runtime puts in the context.

type benchReconcile struct {
	listener      string
	start         time.Time
	writes        int
	alreadyExists int
	notFound      int
}

type benchRecorder struct {
	mu   sync.Mutex
	recs map[types.UID]*benchReconcile
}

func (r *benchRecorder) begin(ctx context.Context, listener string) {
	id := controller.ReconcileIDFromContext(ctx)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.recs[id]; !ok {
		r.recs[id] = &benchReconcile{listener: listener, start: time.Now()}
	}
}

func (r *benchRecorder) write(ctx context.Context, err error) {
	id := controller.ReconcileIDFromContext(ctx)
	if id == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.recs[id]
	if !ok {
		return
	}
	rec.writes++
	switch {
	case kerrors.IsAlreadyExists(err):
		rec.alreadyExists++
	case kerrors.IsNotFound(err):
		rec.notFound++
	}
}

type benchWindow struct {
	reconciles    int
	noWrite       int
	alreadyExists int
	notFound      int
}

func (r *benchRecorder) window(listeners map[string]struct{}, from, to time.Time) benchWindow {
	r.mu.Lock()
	defer r.mu.Unlock()
	var w benchWindow
	for _, rec := range r.recs {
		if _, ok := listeners[rec.listener]; !ok {
			continue
		}
		if rec.start.Before(from) || !rec.start.Before(to) {
			continue
		}
		w.reconciles++
		if rec.writes == 0 {
			w.noWrite++
		}
		w.alreadyExists += rec.alreadyExists
		w.notFound += rec.notFound
	}
	return w
}

type benchClient struct {
	client.Client
	rec *benchRecorder
}

func (c benchClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*v1alpha1.AutoscalingListener); ok {
		c.rec.begin(ctx, key.Name)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c benchClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	err := c.Client.Create(ctx, obj, opts...)
	c.rec.write(ctx, err)
	return err
}

func (c benchClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	err := c.Client.Update(ctx, obj, opts...)
	c.rec.write(ctx, err)
	return err
}

func (c benchClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	err := c.Client.Patch(ctx, obj, patch, opts...)
	c.rec.write(ctx, err)
	return err
}

func (c benchClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	err := c.Client.Delete(ctx, obj, opts...)
	c.rec.write(ctx, err)
	return err
}

// ---------------------------------------------------------------------------
// API server requests the controller's manager sends, informer LIST/WATCH
// excluded, so what is left is what the reconciler itself costs: live secret
// reads and writes. Under the shipped client rate limit this is what queues.

type benchRequests struct{ n atomic.Int64 }

func (c *benchRequests) wrap(rt http.RoundTripper) http.RoundTripper {
	return benchRoundTripper{rt: rt, c: c}
}

type benchRoundTripper struct {
	rt http.RoundTripper
	c  *benchRequests
}

func (r benchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	isInformer := q.Get("watch") == "true" || (req.Method == http.MethodGet && q.Has("resourceVersion") && !strings.Contains(req.URL.Path, "/secrets/"))
	if !isInformer {
		r.c.n.Add(1)
	}
	return r.rt.RoundTrip(req)
}

// ---------------------------------------------------------------------------
// controller-runtime's own per-result counters, for the requeue breakdown.

func benchReconcileResults(t *testing.T) map[string]float64 {
	families, err := crmetrics.Registry.Gather()
	require.NoError(t, err)
	out := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "controller_runtime_reconcile_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			var ctrlName, result string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "controller":
					ctrlName = l.GetValue()
				case "result":
					result = l.GetValue()
				}
			}
			if ctrlName == "autoscalinglistener" {
				out[result] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

func benchResultsDelta(before, after map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range after {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Aggregation.

type benchScenario struct {
	samples   int
	requests  int64
	latencies []time.Duration
	windows   []benchWindow
	results   map[string]float64
}

type benchAggregate struct {
	order     []string
	scenarios map[string]*benchScenario
}

func newBenchAggregate() *benchAggregate {
	return &benchAggregate{scenarios: map[string]*benchScenario{}}
}

func (a *benchAggregate) get(name string) *benchScenario {
	s, ok := a.scenarios[name]
	if !ok {
		s = &benchScenario{results: map[string]float64{}}
		a.scenarios[name] = s
		a.order = append(a.order, name)
	}
	return s
}

func (a *benchAggregate) add(name string, listeners int, requests int64, lat []time.Duration, w benchWindow, results map[string]float64) {
	s := a.get(name)
	s.samples += listeners
	s.requests += requests
	s.latencies = append(s.latencies, lat...)
	s.windows = append(s.windows, w)
	for k, v := range results {
		s.results[k] += v
	}
}

func benchPct(d []time.Duration, p float64) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := slices.Clone(d)
	slices.Sort(s)
	i := int(float64(len(s)-1) * p)
	return s[i]
}

func (a *benchAggregate) print(t *testing.T, variant string, n int) {
	for _, name := range a.order {
		s := a.scenarios[name]
		var total benchWindow
		for _, w := range s.windows {
			total.reconciles += w.reconciles
			total.noWrite += w.noWrite
			total.alreadyExists += w.alreadyExists
			total.notFound += w.notFound
		}
		perListener := func(v int) string {
			return strconv.FormatFloat(float64(v)/float64(s.samples), 'f', 1, 64)
		}
		keys := slices.Sorted(maps.Keys(s.results))
		var res []string
		for _, k := range keys {
			res = append(res, fmt.Sprintf("%s=%s", k, strconv.FormatFloat(s.results[k]/float64(s.samples), 'f', 1, 64)))
		}
		lat := "-"
		if len(s.latencies) > 0 {
			lat = fmt.Sprintf("p50=%v p95=%v max=%v",
				benchPct(s.latencies, 0.5).Round(time.Millisecond),
				benchPct(s.latencies, 0.95).Round(time.Millisecond),
				benchPct(s.latencies, 1).Round(time.Millisecond))
		}
		t.Logf("BENCH variant=%s n=%d scenario=%-26s latency[%s] reconciles/listener=%s noWrite/listener=%s apiRequests/listener=%s alreadyExists=%d notFound=%d results/listener[%s]",
			variant, n, name, lat,
			perListener(total.reconciles), perListener(total.noWrite), perListener(int(s.requests)),
			total.alreadyExists, total.notFound, strings.Join(res, " "))
	}
}

// ---------------------------------------------------------------------------
// One run: fresh namespace, fresh manager, three groups of n listeners.

func runListenerBench(t *testing.T, restCfg *rest.Config, admin client.Client, n int, agg *benchAggregate) {
	ctx := context.Background()

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "bench-" + strings.ToLower(RandStringRunes(6))}}
	require.NoError(t, admin.Create(ctx, ns))
	defer func() { _ = admin.Delete(ctx, ns) }()

	// Production manager settings that matter here: secrets and config maps
	// are read live, and the listener runs with its shipped concurrency.
	// The controller gets the client rate limits it ships with (main.go).
	mgrCfg := rest.CopyConfig(restCfg)
	mgrCfg.QPS = 20
	mgrCfg.Burst = 30
	requests := &benchRequests{}
	mgrCfg.WrapTransport = requests.wrap
	mgr, err := ctrl.NewManager(mgrCfg, ctrl.Options{
		Controller: config.Controller{SkipNameValidation: ptr.To(true)},
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{ns.Name: {}},
		},
		Client: client.Options{
			Cache: &client.CacheOptions{
				DisableFor: []client.Object{&corev1.Secret{}, &corev1.ConfigMap{}},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, SetupIndexers(mgr))

	rec := &benchRecorder{recs: map[types.UID]*benchReconcile{}}
	rc := NewResourceCache()
	reconciler := &AutoscalingListenerReconciler{
		Client: benchClient{Client: mgr.GetClient(), rec: rec},
		Scheme: mgr.GetScheme(),
		Log:    logr.Discard(),
		ResourceBuilder: ResourceBuilder{
			ResourceCache:  &rc,
			SecretResolver: secretresolver.New(mgr.GetClient(), scalefake.NewMultiClient()),
		},
	}
	require.NoError(t, reconciler.SetupWithManager(mgr, WithMaxConcurrentReconciles(OptionsWithDefault().AutoscalingListenerMaxConcurrentReconciles)))

	mgrCtx, cancel := context.WithCancel(ctx)
	g, gctx := errgroup.WithContext(mgrCtx)
	g.Go(func() error { return mgr.Start(gctx) })
	defer func() {
		cancel()
		_ = g.Wait()
	}()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	ghSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-config-secret", Namespace: ns.Name},
		Data:       map[string][]byte{"github_token": []byte(defaultGitHubToken)},
	}
	require.NoError(t, admin.Create(ctx, ghSecret))

	minR, maxR := 1, 10
	ars := &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{Name: "bench-ars", Namespace: ns.Name},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			GitHubConfigUrl:    "https://github.com/owner/repo",
			GitHubConfigSecret: ghSecret.Name,
			MaxRunners:         &maxR,
			MinRunners:         &minR,
			Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "runner", Image: "ghcr.io/actions/runner"}},
			}},
		},
	}
	require.NoError(t, admin.Create(ctx, ars))

	b := &listenerBench{t: t, admin: admin, rec: rec, requests: requests, ns: ns.Name, ars: ars, agg: agg, n: n}

	// Group A: setup, update, stop with the pod held.
	a := b.setup("a", n)
	b.update(a)
	b.stopHeld(a)

	// Group B: setup, delete with the pod held.
	bb := b.setup("b", n)
	b.deleteHeld(bb)

	// Group C: setup, delete with the config secret held by a foreign finalizer.
	c := b.setup("c", n)
	b.deleteSecretHeld(c)
}

type listenerBench struct {
	t        *testing.T
	admin    client.Client
	rec      *benchRecorder
	requests *benchRequests
	n        int
	ns       string
	ars      *v1alpha1.AutoscalingRunnerSet
	agg      *benchAggregate
	reqMark  int64
}

type benchGroup struct {
	listeners []*v1alpha1.AutoscalingListener
	names     map[string]struct{}
}

func (b *listenerBench) record(name string, group *benchGroup, lat []time.Duration, from, to time.Time, before map[string]float64) {
	w := b.rec.window(group.names, from, to)
	now := b.requests.n.Load()
	b.agg.add(name, len(group.listeners), now-b.reqMark, lat, w, benchResultsDelta(before, benchReconcileResults(b.t)))
	b.reqMark = now
}

// mark starts a new request-count window. Anything between the previous
// record and the mark (setup of the next scenario) is not attributed.
func (b *listenerBench) mark() { b.reqMark = b.requests.n.Load() }

// hold is how long a pod or secret is held. It has to outlast the
// controller's first cleanup pass over every listener, which is bound by the
// client rate limit at larger n, or the window never sees steady-state waiting.
func (b *listenerBench) hold(base time.Duration) time.Duration {
	return max(base, time.Duration(b.n)*800*time.Millisecond)
}

func (b *listenerBench) newListener(name string) *v1alpha1.AutoscalingListener {
	return &v1alpha1.AutoscalingListener{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: b.ns},
		Spec: v1alpha1.AutoscalingListenerSpec{
			GitHubConfigURL:               "https://github.com/owner/repo",
			GitHubConfigSecret:            "github-config-secret",
			RunnerScaleSetID:              1,
			AutoscalingRunnerSetNamespace: b.ars.Namespace,
			AutoscalingRunnerSetName:      b.ars.Name,
			EphemeralRunnerSetName:        "bench-ers",
			MaxRunners:                    10,
			MinRunners:                    1,
			Image:                         "ghcr.io/owner/repo",
			ServiceAccountMetadata:        &v1alpha1.ResourceMeta{Annotations: map[string]string{"bench/sa": "initial"}},
			RoleMetadata:                  &v1alpha1.ResourceMeta{Annotations: map[string]string{"bench/role": "initial"}},
			RoleBindingMetadata:           &v1alpha1.ResourceMeta{Annotations: map[string]string{"bench/rb": "initial"}},
		},
	}
}

// waitAll polls until done reports true for every listener, and returns the
// time each one first did, measured from start. snapshot runs once per poll
// and returns the predicate to evaluate against that single read.
func (b *listenerBench) waitAll(group *benchGroup, start time.Time, snapshot func() func(l *v1alpha1.AutoscalingListener) bool) []time.Duration {
	lat := make([]time.Duration, len(group.listeners))
	pending := map[int]struct{}{}
	for i := range group.listeners {
		pending[i] = struct{}{}
	}
	deadline := time.Now().Add(benchTimeout)
	for len(pending) > 0 {
		done := snapshot()
		now := time.Since(start)
		for i := range pending {
			if done(group.listeners[i]) {
				lat[i] = now
				delete(pending, i)
			}
		}
		if time.Now().After(deadline) {
			b.t.Errorf("timed out with %d listeners not converged", len(pending))
			return nil
		}
		time.Sleep(benchPoll)
	}
	return lat
}

func (b *listenerBench) pods() map[string]*corev1.Pod {
	var list corev1.PodList
	require.NoError(b.t, b.admin.List(context.Background(), &list, client.InNamespace(b.ns)))
	out := make(map[string]*corev1.Pod, len(list.Items))
	for i := range list.Items {
		out[list.Items[i].Name] = &list.Items[i]
	}
	return out
}

func (b *listenerBench) listeners() map[string]struct{} {
	var list v1alpha1.AutoscalingListenerList
	require.NoError(b.t, b.admin.List(context.Background(), &list, client.InNamespace(b.ns)))
	out := make(map[string]struct{}, len(list.Items))
	for i := range list.Items {
		out[list.Items[i].Name] = struct{}{}
	}
	return out
}

func (b *listenerBench) pod(l *v1alpha1.AutoscalingListener) (*corev1.Pod, bool) {
	pod := new(corev1.Pod)
	if err := b.admin.Get(context.Background(), client.ObjectKey{Namespace: b.ns, Name: l.Name}, pod); err != nil {
		return nil, false
	}
	return pod, true
}

func (b *listenerBench) setup(prefix string, n int) *benchGroup {
	ctx := context.Background()
	group := &benchGroup{names: map[string]struct{}{}}
	for i := range n {
		l := b.newListener(fmt.Sprintf("%s-%d", prefix, i))
		group.listeners = append(group.listeners, l)
		group.names[l.Name] = struct{}{}
	}

	b.mark()
	before := benchReconcileResults(b.t)
	start := time.Now()
	for _, l := range group.listeners {
		require.NoError(b.t, b.admin.Create(ctx, l))
	}
	lat := b.waitAll(group, start, func() func(l *v1alpha1.AutoscalingListener) bool {
		pods := b.pods()
		return func(l *v1alpha1.AutoscalingListener) bool {
			_, ok := pods[l.Name]
			return ok
		}
	})
	if b.n == 1 {
		b.t.Logf("BENCHRAW setup group=%s latency=%v", prefix, lat)
	}
	time.Sleep(benchSettle)
	b.record("setup (create→pod)", group, lat, start, time.Now(), before)
	return group
}

func (b *listenerBench) update(group *benchGroup) {
	ctx := context.Background()
	oldPods := map[string]*corev1.Pod{}
	for _, l := range group.listeners {
		pod, ok := b.pod(l)
		require.True(b.t, ok)
		oldPods[l.Name] = pod
	}

	b.mark()
	before := benchReconcileResults(b.t)
	start := time.Now()
	for _, l := range group.listeners {
		current := new(v1alpha1.AutoscalingListener)
		require.NoError(b.t, b.admin.Get(ctx, client.ObjectKeyFromObject(l), current))
		original := current.DeepCopy()
		current.Spec.MaxRunners = 20
		current.Spec.ServiceAccountMetadata.Annotations["bench/sa"] = "updated"
		current.Spec.RoleMetadata.Annotations["bench/role"] = "updated"
		current.Spec.RoleBindingMetadata.Annotations["bench/rb"] = "updated"
		require.NoError(b.t, b.admin.Patch(ctx, current, client.MergeFrom(original)))
	}
	lat := b.waitAll(group, start, func() func(l *v1alpha1.AutoscalingListener) bool {
		pods := b.pods()
		return func(l *v1alpha1.AutoscalingListener) bool {
			pod, ok := pods[l.Name]
			if !ok || pod.UID == oldPods[l.Name].UID {
				return false
			}
			return pod.Annotations[AnnotationKeyListenerConfigResourceVersion] != oldPods[l.Name].Annotations[AnnotationKeyListenerConfigResourceVersion]
		}
	})
	time.Sleep(benchSettle)
	b.record("update (patch→new pod)", group, lat, start, time.Now(), before)
}

func (b *listenerBench) holdPods(group *benchGroup) {
	ctx := context.Background()
	for _, l := range group.listeners {
		pod, ok := b.pod(l)
		require.True(b.t, ok)
		original := pod.DeepCopy()
		controllerutil.AddFinalizer(pod, benchPodHoldFinalizer)
		require.NoError(b.t, b.admin.Patch(ctx, pod, client.MergeFrom(original)))
	}
	// Let the finalizer's own update event play out, outside any window.
	time.Sleep(benchSettle)
}

func (b *listenerBench) releasePods(group *benchGroup) {
	ctx := context.Background()
	for _, l := range group.listeners {
		pod, ok := b.pod(l)
		if !ok {
			continue
		}
		original := pod.DeepCopy()
		controllerutil.RemoveFinalizer(pod, benchPodHoldFinalizer)
		require.NoError(b.t, client.IgnoreNotFound(b.admin.Patch(ctx, pod, client.MergeFrom(original))))
	}
}

func (b *listenerBench) stopHeld(group *benchGroup) {
	ctx := context.Background()
	b.holdPods(group)

	b.mark()
	before := benchReconcileResults(b.t)
	start := time.Now()
	for _, l := range group.listeners {
		current := new(v1alpha1.AutoscalingListener)
		require.NoError(b.t, b.admin.Get(ctx, client.ObjectKeyFromObject(l), current))
		original := current.DeepCopy()
		current.Spec.Phase = v1alpha1.AutoscalingListenerPhaseStopped
		require.NoError(b.t, b.admin.Patch(ctx, current, client.MergeFrom(original)))
	}
	time.Sleep(b.hold(benchHold))
	released := time.Now()
	b.record("stop: pod terminating", group, nil, start, released, before)

	before = benchReconcileResults(b.t)
	b.releasePods(group)
	lat := b.waitAll(group, released, func() func(l *v1alpha1.AutoscalingListener) bool {
		pods := b.pods()
		return func(l *v1alpha1.AutoscalingListener) bool {
			_, ok := pods[l.Name]
			return !ok
		}
	})
	time.Sleep(benchSettle)
	b.record("stop: after pod gone", group, lat, released, time.Now(), before)
}

func (b *listenerBench) listenerGone() func(l *v1alpha1.AutoscalingListener) bool {
	present := b.listeners()
	return func(l *v1alpha1.AutoscalingListener) bool {
		_, ok := present[l.Name]
		return !ok
	}
}

func (b *listenerBench) deleteHeld(group *benchGroup) {
	ctx := context.Background()
	b.holdPods(group)

	b.mark()
	before := benchReconcileResults(b.t)
	start := time.Now()
	for _, l := range group.listeners {
		require.NoError(b.t, b.admin.Delete(ctx, l))
	}
	time.Sleep(b.hold(benchHold))
	released := time.Now()
	b.record("delete: pod terminating", group, nil, start, released, before)

	before = benchReconcileResults(b.t)
	b.releasePods(group)
	lat := b.waitAll(group, released, b.listenerGone)
	time.Sleep(benchSettle)
	b.record("delete: pod gone→gone", group, lat, released, time.Now(), before)
}

func (b *listenerBench) deleteSecretHeld(group *benchGroup) {
	ctx := context.Background()
	secretFor := func(l *v1alpha1.AutoscalingListener) client.ObjectKey {
		return client.ObjectKey{Namespace: b.ns, Name: scaleSetListenerConfigName(l)}
	}

	b.mark()
	before := benchReconcileResults(b.t)
	start := time.Now()
	for _, l := range group.listeners {
		secret := new(corev1.Secret)
		require.NoError(b.t, b.admin.Get(ctx, secretFor(l), secret))
		original := secret.DeepCopy()
		controllerutil.AddFinalizer(secret, benchPodHoldFinalizer)
		require.NoError(b.t, b.admin.Patch(ctx, secret, client.MergeFrom(original)))
		require.NoError(b.t, b.admin.Delete(ctx, l))
	}
	time.Sleep(b.hold(benchSecretHold))
	released := time.Now()
	b.record("delete: secret held", group, nil, start, released, before)

	before = benchReconcileResults(b.t)
	for _, l := range group.listeners {
		secret := new(corev1.Secret)
		if err := b.admin.Get(ctx, secretFor(l), secret); err != nil {
			continue
		}
		original := secret.DeepCopy()
		controllerutil.RemoveFinalizer(secret, benchPodHoldFinalizer)
		require.NoError(b.t, client.IgnoreNotFound(b.admin.Patch(ctx, secret, client.MergeFrom(original))))
	}
	lat := b.waitAll(group, released, b.listenerGone)
	time.Sleep(benchSettle)
	b.record("delete: secret freed→gone", group, lat, released, time.Now(), before)
}

func benchEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}
