// Package test_e2e_cachevolume runs the runner cache volume feature against a real
// Kubernetes cluster that has a CSI driver with snapshot support.
//
// The real EphemeralRunner and cache snapshot controllers run in the test process;
// only the GitHub Actions service is faked. Runner pods are plain busybox containers.
//
// It is skipped unless ARC_CACHE_E2E=1. It refuses to run against any kubeconfig
// context that is not a kind cluster (context name starting with "kind-").
//
// Prerequisites in the cluster:
//   - the ARC CRDs (kubectl apply --server-side -f config/crd/bases/actions.github.com_*.yaml)
//   - VolumeSnapshot CRDs and the snapshot controller
//   - a CSI driver with snapshot support, a StorageClass (ARC_CACHE_E2E_STORAGE_CLASS,
//     default csi-hostpath-sc) and a default VolumeSnapshotClass
package test_e2e_cachevolume

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/apis/actions.github.com/v1alpha1"
	actionsgithubcom "github.com/actions/actions-runner-controller/controllers/actions.github.com"
	scalefake "github.com/actions/actions-runner-controller/controllers/actions.github.com/multiclient/fake"
	"github.com/actions/actions-runner-controller/controllers/actions.github.com/secretresolver"
	"github.com/actions/scaleset"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	busybox = "registry.k8s.io/e2e-test-images/busybox:1.36.1-1"
	// lineageLabel is actionsgithubcom.LabelKeyCacheLineage.
	lineageLabel = actionsgithubcom.LabelKeyCacheLineage
)

// jobScript is the "job" of a runner pod. It prints what it restored, fails if the previous
// job's data is missing, and appends its own marker.
const jobScript = `
echo "restored: $(tr '\n' ' ' < /cache/log 2>/dev/null)"
if [ "$JOB_PREV" != "" ]; then grep -q "^$JOB_PREV$" /cache/log || { echo "MISSING $JOB_PREV"; exit 3; }; fi
echo "$JOB_NAME" >> /cache/log
sync
`

type stopwatch struct {
	t     *testing.T
	start time.Time
}

func (s stopwatch) lap(what string) time.Duration {
	d := time.Since(s.start).Round(100 * time.Millisecond)
	s.t.Logf("  +%-8v %s", d, what)
	return d
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := cond()
		if err == nil && ok {
			return
		}
		lastErr = err
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s (last error: %v)", timeout, what, lastErr)
}

type env struct {
	t       *testing.T
	ctx     context.Context
	c       client.Client
	cs      kubernetes.Interface
	ns      string
	sc      string
	lineage string
	ersUID  string
}

func (e *env) snapshots() []snapshotv1.VolumeSnapshot {
	var list snapshotv1.VolumeSnapshotList
	require.NoError(e.t, e.c.List(e.ctx, &list, client.InNamespace(e.ns), client.MatchingLabels{lineageLabel: e.lineage}))
	return list.Items
}

func (e *env) snapshotNames() []string {
	var out []string
	for _, s := range e.snapshots() {
		state := "pending"
		if s.Status != nil && s.Status.ReadyToUse != nil && *s.Status.ReadyToUse {
			state = "ready"
		}
		out = append(out, s.Name+"("+state+")")
	}
	return out
}

func (e *env) snapshotReady(name string) bool {
	var s snapshotv1.VolumeSnapshot
	if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: name}, &s); err != nil {
		return false
	}
	return s.Status != nil && s.Status.ReadyToUse != nil && *s.Status.ReadyToUse
}

func (e *env) newRunner(job, prev string) *v1alpha1.EphemeralRunner {
	env := []corev1.EnvVar{{Name: "JOB_NAME", Value: job}, {Name: "JOB_PREV", Value: prev}}
	return &v1alpha1.EphemeralRunner{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "set-runner-" + job,
			Namespace: e.ns,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: v1alpha1.GroupVersion.String(), Kind: "EphemeralRunnerSet", Name: "set",
				UID: "" /* set below */, Controller: ptr.To(true), BlockOwnerDeletion: ptr.To(true),
			}},
		},
		Spec: v1alpha1.EphemeralRunnerSpec{
			GitHubConfigURL:    "https://github.com/owner/repo",
			GitHubConfigSecret: "github-config-secret",
			RunnerScaleSetID:   1,
			CacheVolume:        &v1alpha1.CacheVolume{StorageClassName: e.sc, Size: resource.MustParse("1Gi")},
			PodTemplateSpec: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
				RestartPolicy: corev1.RestartPolicyNever, // as in real runner templates
				Containers: []corev1.Container{{
					Name:         v1alpha1.EphemeralRunnerContainerName,
					Image:        busybox,
					Command:      []string{"sh", "-c", jobScript},
					Env:          env,
					VolumeMounts: []corev1.VolumeMount{{Name: "cache", MountPath: "/cache"}},
				}},
			}},
		},
	}
}

// runJob creates a runner and waits for it to finish, returning the lap timings.
func (e *env) startJob(job, prev string) stopwatch {
	t := e.t
	er := e.newRunner(job, prev)
	er.OwnerReferences[0].UID = types.UID(e.ersUID)
	sw := stopwatch{t: t, start: time.Now()}
	require.NoError(t, e.c.Create(e.ctx, er))
	t.Logf("job %s: runner created, snapshots before: %v", job, e.snapshotNames())
	return sw
}

func (e *env) waitJob(job string, sw stopwatch) {
	t := e.t
	name := "set-runner-" + job
	claim := name + "-cache"
	var boundSeen, podSeen, runningSeen bool
	eventually(t, 4*time.Minute, "runner "+name+" to finish and be removed", func() (bool, error) {
		var pvc corev1.PersistentVolumeClaim
		if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: claim}, &pvc); err == nil && pvc.Status.Phase == corev1.ClaimBound && !boundSeen {
			boundSeen = true
			src := "empty"
			if pvc.Spec.DataSource != nil {
				src = "restored from " + pvc.Spec.DataSource.Name
			}
			sw.lap("claim bound (" + src + ")")
		}
		var pod corev1.Pod
		if err := e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: name}, &pod); err == nil {
			if !podSeen {
				podSeen = true
				sw.lap("pod created")
			}
			if !runningSeen && (pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodSucceeded) {
				runningSeen = true
				sw.lap("pod running/finished (volume attached+mounted)")
			}
		}
		err := e.c.Get(e.ctx, client.ObjectKey{Namespace: e.ns, Name: name}, new(v1alpha1.EphemeralRunner))
		if kerrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	sw.lap("runner removed")
}

func (e *env) waitSnapshot(job string, sw stopwatch) {
	name := "set-runner-" + job + "-cache"
	eventually(e.t, 3*time.Minute, "snapshot "+name+" to be ready", func() (bool, error) { return e.snapshotReady(name), nil })
	sw.lap("snapshot " + name + " ready")
}

func TestCacheVolumeOnRealCluster(t *testing.T) {
	if os.Getenv("ARC_CACHE_E2E") != "1" {
		t.Skip("set ARC_CACHE_E2E=1 to run against a kind cluster")
	}

	rawCfg, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(rawCfg.CurrentContext, "kind-"),
		"refusing to run against context %q: only kind clusters are allowed", rawCfg.CurrentContext)
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(clientcmd.NewDefaultClientConfigLoadingRules(), nil).ClientConfig()
	require.NoError(t, err)
	t.Logf("cluster context: %s", rawCfg.CurrentContext)

	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.WriteTo(os.Stderr)))
	scheme := clientgoscheme.Scheme
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, snapshotv1.AddToScheme(scheme))

	ns := "cache-e2e-" + strings.ToLower(rand.String(5))
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	require.NoError(t, err)
	cs, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}))
	t.Cleanup(func() {
		if os.Getenv("ARC_CACHE_E2E_KEEP") == "1" {
			t.Logf("keeping namespace %s", ns)
			return
		}
		_ = c.Delete(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})

	sc := os.Getenv("ARC_CACHE_E2E_STORAGE_CLASS")
	if sc == "" {
		sc = "csi-hostpath-sc"
	}
	e := &env{t: t, ctx: ctx, c: c, cs: cs, ns: ns, sc: sc, lineage: lineageOf(ns, "set")}

	require.NoError(t, c.Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "github-config-secret", Namespace: ns},
		Data:       map[string][]byte{"github_token": []byte("fake")},
	}))
	ars := &v1alpha1.AutoscalingRunnerSet{
		ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: ns},
		Spec: v1alpha1.AutoscalingRunnerSetSpec{
			CacheVolume: &v1alpha1.CacheVolume{StorageClassName: sc, Size: resource.MustParse("1Gi")},
			Template:    placeholderTemplate(),
		},
	}
	require.NoError(t, c.Create(ctx, ars))
	// No ARC controller reconciles the set objects in this test: they only own the runners.
	ers := &v1alpha1.EphemeralRunnerSet{
		ObjectMeta: metav1.ObjectMeta{Name: "set", Namespace: ns},
		Spec:       v1alpha1.EphemeralRunnerSetSpec{EphemeralRunnerSpec: v1alpha1.EphemeralRunnerSpec{PodTemplateSpec: placeholderTemplate()}},
	}
	require.NoError(t, c.Create(ctx, ers))
	e.ersUID = string(ers.UID)

	// The real controllers, scoped to the test namespace, with only the Actions service faked.
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		Cache:   cache.Options{DefaultNamespaces: map[string]cache.Config{ns: {}}},
	})
	require.NoError(t, err)

	resourceCache := actionsgithubcom.NewResourceCache()
	fakeActions := scalefake.NewMultiClient(scalefake.WithClient(scalefake.NewClient(
		scalefake.WithGenerateJitRunnerConfig(&scaleset.RunnerScaleSetJitRunnerConfig{
			Runner:           &scaleset.RunnerReference{ID: 1, Name: "test-runner"},
			EncodedJITConfig: "fake-jit-config",
		}, nil),
	)))
	require.NoError(t, (&actionsgithubcom.EphemeralRunnerReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("EphemeralRunner"),
		ResourceBuilder: actionsgithubcom.ResourceBuilder{
			ResourceCache:  &resourceCache,
			Scheme:         mgr.GetScheme(),
			SecretResolver: secretresolver.New(mgr.GetClient(), fakeActions),
		},
	}).SetupWithManager(mgr))
	require.NoError(t, (&actionsgithubcom.CacheSnapshotReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Log:    ctrl.Log.WithName("CacheSnapshot"),
	}).SetupWithManager(mgr))

	go func() { _ = mgr.Start(ctx) }()
	require.True(t, mgr.GetCache().WaitForCacheSync(ctx))

	// 1. Three sequential jobs. Each must see the data of the previous one.
	for i, prev := 1, ""; i <= 3; i++ {
		job := fmt.Sprintf("run-%d", i)
		t.Logf("=== job %s (expects %q restored)", job, prev)
		sw := e.startJob(job, prev)
		e.waitJob(job, sw)
		e.waitSnapshot(job, sw)
		prev = job
	}

	// 2. GC keeps only the newest snapshot.
	gcWatch := stopwatch{t: t, start: time.Now()}
	eventually(t, 5*time.Minute, "only the newest snapshot to remain", func() (bool, error) {
		snaps := e.snapshots()
		return len(snaps) == 1 && snaps[0].Name == "set-runner-run-3-cache", nil
	})
	gcWatch.lap(fmt.Sprintf("GC done, remaining: %v", e.snapshotNames()))

	// 3. Two concurrent jobs restore the same base; the last one to become ready wins.
	t.Logf("=== concurrent jobs run-4a and run-4b (both expect run-3)")
	swA, swB := e.startJob("run-4a", "run-3"), e.startJob("run-4b", "run-3")
	e.waitJob("run-4a", swA)
	e.waitJob("run-4b", swB)
	e.waitSnapshot("run-4a", swA)
	e.waitSnapshot("run-4b", swB)
	eventually(t, 5*time.Minute, "exactly one snapshot after concurrent jobs", func() (bool, error) { return len(e.snapshots()) == 1, nil })
	t.Logf("remaining after concurrent jobs: %v", e.snapshotNames())

	// 4. Claims of finished runners are cleaned up.
	eventually(t, 3*time.Minute, "cache claims of finished runners to be removed", func() (bool, error) {
		var pvcs corev1.PersistentVolumeClaimList
		if err := c.List(ctx, &pvcs, client.InNamespace(ns), client.MatchingLabels{lineageLabel: e.lineage}); err != nil {
			return false, err
		}
		return len(pvcs.Items) == 0, nil
	})

	// 5. The surviving snapshot holds the whole history: restore it in a plain pod and read it.
	final := e.snapshots()[0]
	require.NoError(t, c.Create(ctx, &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "verify", Namespace: ns},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &sc,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")}},
			DataSource:       &corev1.TypedLocalObjectReference{APIGroup: ptr.To("snapshot.storage.k8s.io"), Kind: "VolumeSnapshot", Name: final.Name},
		},
	}))
	require.NoError(t, c.Create(ctx, &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "verify", Namespace: ns},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{Name: "c", Image: busybox, Command: []string{"cat", "/cache/log"},
				VolumeMounts: []corev1.VolumeMount{{Name: "v", MountPath: "/cache"}}}},
			Volumes: []corev1.Volume{{Name: "v", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "verify"}}}},
		},
	}))
	eventually(t, 3*time.Minute, "verify pod to finish", func() (bool, error) {
		var p corev1.Pod
		if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: "verify"}, &p); err != nil {
			return false, err
		}
		return p.Status.Phase == corev1.PodSucceeded, nil
	})
	rc, err := cs.CoreV1().Pods(ns).GetLogs("verify", &corev1.PodLogOptions{}).Stream(ctx)
	require.NoError(t, err)
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	require.NoError(t, err)
	lines := strings.Fields(string(raw))
	t.Logf("final cache log (from snapshot %s): %v", final.Name, lines)
	require.Equal(t, []string{"run-1", "run-2", "run-3"}, lines[:3], "history must be chained through snapshots")
	require.Len(t, lines, 4, "exactly one of the concurrent jobs wins (last writer wins)")
	require.Contains(t, []string{"run-4a", "run-4b"}, lines[3])
}

// lineageOf mirrors the lineage label value ARC derives from a scale set.
func lineageOf(namespace, scaleSet string) string {
	sum := sha256.Sum256([]byte(namespace + "/" + scaleSet))
	return hex.EncodeToString(sum[:])[:20]
}

func placeholderTemplate() corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: v1alpha1.EphemeralRunnerContainerName, Image: busybox}}}}
}
