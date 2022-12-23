package testing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/actions/actions-runner-controller/testing/runtime"
)

type T = testing.T

var Short = testing.Short

var images = map[string]string{
	"1.22": "kindest/node:v1.22.9@sha256:8135260b959dfe320206eb36b3aeda9cffcb262f4b44cda6b33f7bb73f453105",
	"1.23": "kindest/node:v1.23.6@sha256:b1fa224cc6c7ff32455e0b1fd9cbfd3d3bc87ecaa8fcb06961ed1afb3db0f9ae",
	"1.24": "kindest/node:v1.24.0@sha256:0866296e693efe1fed79d5e6c7af8df71fc73ae45e3679af05342239cdc5bc8e",
}

func Img(repo, tag string) ContainerImage {
	return ContainerImage{
		Repo: repo,
		Tag:  tag,
	}
}

// Env is a testing environment.
// All of its methods are idempotent so that you can safely call it from within each subtest
// and you can rerun the individual subtest until it works as you expect.
type Env struct {
	Kubeconfig string
	docker     *Docker
	Kubectl    *Kubectl
	bash       *Bash
}

func Start(t *testing.T, k8sMinorVer string) *Env {
	t.Helper()

	var env Env

	d := &Docker{}

	env.docker = d

	kctl := &Kubectl{}

	env.Kubectl = kctl

	bash := &Bash{}

	env.bash = bash

	return &env
}

func (e *Env) GetOrGenerateTestID(t *testing.T) string {
	kctl := e.Kubectl

	cmKey := "id"

	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cmCfg := KubectlConfig{
		Env: kubectlEnv,
	}
	testInfoName := "test-info"

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	m, _ := kctl.GetCMLiterals(ctx, testInfoName, cmCfg)

	if m == nil {
		id := RandStringBytesRmndr(10)
		m = map[string]string{cmKey: id}
		if err := kctl.CreateCMLiterals(ctx, testInfoName, m, cmCfg); err != nil {
			t.Fatal(err)
		}
	}

	return m[cmKey]
}

func (e *Env) DeleteTestID(t *testing.T) {
	kctl := e.Kubectl

	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cmCfg := KubectlConfig{
		Env: kubectlEnv,
	}
	testInfoName := "test-info"

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	if err := kctl.DeleteCM(ctx, testInfoName, cmCfg); err != nil {
		t.Fatal(err)
	}
}

func (e *Env) DockerBuild(t *testing.T, builds []DockerBuild) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 450*time.Second)
	defer cancel()

	if err := e.docker.Build(ctx, builds); err != nil {
		t.Fatal(err)
	}
}

func (e *Env) DockerPush(t *testing.T, images []ContainerImage) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	if err := e.docker.Push(ctx, images); err != nil {
		t.Fatal(err)
	}
}

func (e *Env) KubectlApply(t *testing.T, path string, cfg KubectlConfig) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cfg.Env = append(kubectlEnv, cfg.Env...)

	if err := e.Kubectl.Apply(ctx, path, cfg); err != nil {
		t.Fatal(err)
	}
}

func (e *Env) KubectlWaitUntilDeployAvailable(t *testing.T, name string, cfg KubectlConfig) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cfg.Env = append(kubectlEnv, cfg.Env...)

	if err := e.Kubectl.WaitUntilDeployAvailable(ctx, name, cfg); err != nil {
		t.Fatal(err)
	}
}

func (e *Env) KubectlEnsureNS(t *testing.T, name string, cfg KubectlConfig) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cfg.Env = append(kubectlEnv, cfg.Env...)

	if err := e.Kubectl.EnsureNS(ctx, name, cfg); err != nil {
		t.Fatal(err)
	}
}

func (e *Env) KubectlEnsureClusterRoleBindingServiceAccount(t *testing.T, bindingName string, clusterrole string, serviceaccount string, cfg KubectlConfig) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	kubectlEnv := []string{
		"KUBECONFIG=" + e.Kubeconfig,
	}

	cfg.Env = append(kubectlEnv, cfg.Env...)

	if _, err := e.Kubectl.GetClusterRoleBinding(ctx, bindingName, cfg); err != nil {
		if err := e.Kubectl.CreateClusterRoleBindingServiceAccount(ctx, bindingName, clusterrole, serviceaccount, cfg); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *Env) RunScript(t *testing.T, path string, cfg ScriptConfig) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	if err := e.bash.RunScript(ctx, path, cfg); err != nil {
		t.Fatal(err)
	}
}

// Kind is a test cluster backend by a kind cluster and the dockerd powering it.
// It intracts with the kind cluster via the kind command and dockerd via the docker command
// for various operations that otherwise needs to be automated via shell scripts or makefiles.
type Kind struct {
	// Name is the name of the cluster
	Name string

	// Dir is the path to the directory that contains various temporary files like a kind cluster config yaml for testing.
	// This is occasionally the value returned by testing.TempDir() so that
	// you don't need to clean it up yourself.
	Dir string

	kubeconfig string

	runtime.Cmdr
}

type Config struct {
	// PreloadImages is the list of container images to be pulled and loaded into the cluster.
	// This might be useful to speed up your test by avoiding to let dockerd pull images from the internet each time you need to
	// run tests.
	PreloadImages []ContainerImage
}

type Option = func(*Config)

func Preload(imgs ...ContainerImage) Option {
	return func(c *Config) {
		c.PreloadImages = append(c.PreloadImages, imgs...)
	}
}

type ContainerImage struct {
	Repo, Tag string
}

func StartKind(t *testing.T, k8sMinorVer string, opts ...Option) *Kind {
	t.Helper()

	invalidChars := []string{"/"}

	name := strings.ToLower(t.Name())

	for _, c := range invalidChars {
		name = strings.ReplaceAll(name, c, "")
	}
	var k Kind
	k.Name = name
	k.Dir = t.TempDir()

	kk := &k
	if err := kk.Start(context.Background(), k8sMinorVer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var run string
		for i := range os.Args {
			// `go test -run $RUN` results in `/tmp/path/to/some.test -test.run $RUN` being run,
			// and hence we check for -test.run
			if os.Args[i] == "-test.run" {
				runIdx := i + 1
				run = os.Args[runIdx]
				break
			} else if strings.HasPrefix(os.Args[i], "-test.run=") {
				split := strings.Split(os.Args[i], "-test.run=")
				run = split[1]
			}
		}

		if t.Failed() {
			return
		}

		// Do not delete the cluster so that we can accelerate interation on tests
		if run != "" && run != "^"+t.Name()+"$" {
			// This should be printed to the debug console for visibility
			t.Logf("Skipped stopping cluster due to run being %q", run)
			return
		}

		kk.Stop(context.Background())
	})

	var cfg Config

	for _, o := range opts {
		o(&cfg)
	}

	if err := k.PullImages(context.Background(), cfg.PreloadImages); err != nil {
		t.Fatal(err)
	}

	if err := k.LoadImages(context.Background(), cfg.PreloadImages); err != nil {
		t.Fatal(err)
	}

	if err := k.writeKubeconfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	return kk
}

func (k *Kind) Kubeconfig() string {
	return k.kubeconfig
}

func (k *Kind) Start(ctx context.Context, k8sMinorVer string) error {
	getNodes, err := k.CombinedOutput(k.kindGetNodesCmd(ctx, k.Name))
	if err != nil {
		return err
	}

	getNodes = strings.TrimSpace(getNodes)

	if getNodes == fmt.Sprintf("No kind nodes found for cluster %q.", k.Name) {
		f, err := os.CreateTemp(k.Dir, k.Name+".kind.yaml")
		if err != nil {
			return err
		}

		image := images[k8sMinorVer]

		kindConfig := []byte(fmt.Sprintf(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: %s
networking:
  apiServerAddress: 0.0.0.0
nodes:
  - role: control-plane
    image: %s
  - role: worker
    image: %s
`, k.Name, image, image))

		if err := os.WriteFile(f.Name(), kindConfig, 0644); err != nil {
			return err
		}

		if _, err := k.CombinedOutput(k.kindCreateCmd(ctx, k.Name, f.Name())); err != nil {
			return err
		}
	}

	return nil
}

func (k *Kind) kindGetNodesCmd(ctx context.Context, cluster string) *exec.Cmd {
	return exec.CommandContext(ctx, "kind", "get", "nodes", "--name", cluster)
}

func (k *Kind) kindCreateCmd(ctx context.Context, cluster, configFile string) *exec.Cmd {
	return exec.CommandContext(ctx, "kind", "create", "cluster", "--name", cluster, "--config", configFile)
}

func (k *Kind) LoadImages(ctx context.Context, images []ContainerImage) error {
	for _, img := range images {
		const maxRetries = 5

		wd, err := os.Getwd()
		if err != nil {
			return err
		}

		tmpDir := filepath.Join(wd, ".testing", k.Name)
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return err
		}
		defer func() {
			if tmpDir != "" && tmpDir != "/" {
				os.RemoveAll(tmpDir)
			}
		}()

		for i := 0; i <= maxRetries; i++ {
			out, err := k.CombinedOutput(k.kindLoadDockerImageCmd(ctx, k.Name, img.Repo, img.Tag, tmpDir))

			out = strings.TrimSpace(out)

			if out == fmt.Sprintf("ERROR: no nodes found for cluster %q", k.Name) {
				time.Sleep(1 * time.Second)
				continue
			}

			if err != nil {
				return fmt.Errorf("failed loading %v: %w", img, err)
			}

			break
		}
	}

	return nil
}

func (k *Kind) kindLoadDockerImageCmd(ctx context.Context, cluster, repo, tag, tmpDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "kind", "--loglevel=trace", "load", "docker-image", repo+":"+tag, "--name", cluster)
	cmd.Env = os.Environ()
	// Set TMPDIR to somewhere under $HOME when you use docker installed with Ubuntu snap
	// Otherwise `load docker-image` fail while running `docker save`.
	// See https://kind.sigs.k8s.io/docs/user/known-issues/#docker-installed-with-snap
	//
	// In other words, it avoids errors like the below `docker save`:
	//   ERROR: command "docker save -o /tmp/image-tar330828066/image.tar quay.io/jetstack/cert-manager-controller:v1.1.1" failed with error: exit status 1
	//   failed to save image: invalid output path: directory "/tmp/image-tar330828066" does not exist
	cmd.Env = append(cmd.Env, "TMPDIR="+tmpDir)

	return cmd
}

func (k *Kind) PullImages(ctx context.Context, images []ContainerImage) error {
	for _, img := range images {
		_, err := k.CombinedOutput(k.dockerPullCmd(ctx, img.Repo, img.Tag))
		if err != nil {
			return err
		}
	}

	return nil
}

func (k *Kind) dockerPullCmd(ctx context.Context, repo, tag string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", "pull", repo+":"+tag)
}

func (k *Kind) Stop(ctx context.Context) error {
	if err := k.kindDeleteCmd(ctx, k.Name).Run(); err != nil {
		return err
	}

	return nil
}

func (k *Kind) kindDeleteCmd(ctx context.Context, cluster string) *exec.Cmd {
	return exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", cluster)
}

func (k *Kind) writeKubeconfig(ctx context.Context) error {
	var err error

	k.kubeconfig, err = filepath.Abs(filepath.Join(k.Dir, "kubeconfig"))
	if err != nil {
		return err
	}

	if err := k.kindExportKubeconfigCmd(ctx, k.Name, k.kubeconfig).Run(); err != nil {
		return err
	}

	return nil
}

func (k *Kind) kindExportKubeconfigCmd(ctx context.Context, cluster, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "kind", "export", "kubeconfig", "--name", cluster)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "KUBECONFIG="+path)

	return cmd
}
