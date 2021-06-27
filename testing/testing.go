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
)

type T = testing.T

var Short = testing.Short

// Cluster is a test cluster backend by a kind cluster and the dockerd powering it.
// It intracts with the kind cluster via the kind command and dockerd via the docker command
// for various operations that otherwise needs to be automated via shell scripts or makefiles.
type Cluster struct {
	// Name is the name of the cluster
	Name string

	// Dir is the path to the directory that contains various temporary files like a kind cluster config yaml for testing.
	// This is occasionally the value returned by testing.TempDir() so that
	// you don't need to clean it up yourself.
	Dir string

	kubeconfig string
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

func Start(t *testing.T, k Cluster, opts ...Option) *Cluster {
	t.Helper()

	invalidChars := []string{"/"}

	name := strings.ToLower(t.Name())

	for _, c := range invalidChars {
		name = strings.ReplaceAll(name, c, "")
	}

	k.Name = name
	k.Dir = t.TempDir()

	kk := &k
	if err := kk.Start(context.Background()); err != nil {
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

func (k *Cluster) Kubeconfig() string {
	return k.kubeconfig
}

func (k *Cluster) Start(ctx context.Context) error {
	getNodes, err := k.combinedOutput(k.kindGetNodesCmd(ctx, k.Name))
	if err != nil {
		return err
	}

	getNodes = strings.TrimSpace(getNodes)

	if getNodes == fmt.Sprintf("No kind nodes found for cluster %q.", k.Name) {
		f, err := os.CreateTemp(k.Dir, k.Name+".kind.yaml")
		if err != nil {
			return err
		}

		kindConfig := []byte(fmt.Sprintf(`kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: %s
`, k.Name))

		if err := os.WriteFile(f.Name(), kindConfig, 0644); err != nil {
			return err
		}

		if _, err := k.combinedOutput(k.kindCreateCmd(ctx, k.Name, f.Name())); err != nil {
			return err
		}
	}

	return nil
}

func (k *Cluster) combinedOutput(cmd *exec.Cmd) (string, error) {
	o, err := cmd.CombinedOutput()
	if err != nil {
		args := append([]string{}, cmd.Args...)
		args[0] = cmd.Path

		cs := strings.Join(args, " ")
		s := string(o)
		k.errorf("%s failed with output:\n%s", cs, s)

		return s, err
	}

	return string(o), nil
}

func (k *Cluster) errorf(f string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, f+"\n", args...)
}

func (k *Cluster) kindGetNodesCmd(ctx context.Context, cluster string) *exec.Cmd {
	return exec.CommandContext(ctx, "kind", "get", "nodes", "--name", cluster)
}

func (k *Cluster) kindCreateCmd(ctx context.Context, cluster, configFile string) *exec.Cmd {
	return exec.CommandContext(ctx, "kind", "create", "cluster", "--name", cluster, "--config", configFile)
}

type DockerBuild struct {
	Dockerfile string
	Args       []BuildArg
	Image      ContainerImage
}

type BuildArg struct {
	Name, Value string
}

func (k *Cluster) BuildImages(ctx context.Context, builds []DockerBuild) error {
	for _, build := range builds {
		var args []string
		args = append(args, "--build-arg=TARGETPLATFORM="+"linux/amd64")
		for _, buildArg := range build.Args {
			args = append(args, "--build-arg="+buildArg.Name+"="+buildArg.Value)
		}
		_, err := k.combinedOutput(k.dockerBuildCmd(ctx, build.Dockerfile, build.Image.Repo, build.Image.Tag, args))

		if err != nil {
			return fmt.Errorf("failed building %v: %w", build, err)
		}
	}

	return nil
}

func (k *Cluster) dockerBuildCmd(ctx context.Context, dockerfile, repo, tag string, args []string) *exec.Cmd {
	buildContext := filepath.Dir(dockerfile)
	args = append([]string{"build", "--tag", repo + ":" + tag, "-f", dockerfile, buildContext}, args...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd
}

func (k *Cluster) LoadImages(ctx context.Context, images []ContainerImage) error {
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
			out, err := k.combinedOutput(k.kindLoadDockerImageCmd(ctx, k.Name, img.Repo, img.Tag, tmpDir))

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

func (k *Cluster) kindLoadDockerImageCmd(ctx context.Context, cluster, repo, tag, tmpDir string) *exec.Cmd {
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

func (k *Cluster) PullImages(ctx context.Context, images []ContainerImage) error {
	for _, img := range images {
		_, err := k.combinedOutput(k.dockerPullCmd(ctx, img.Repo, img.Tag))
		if err != nil {
			return err
		}
	}

	return nil
}

func (k *Cluster) dockerPullCmd(ctx context.Context, repo, tag string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", "pull", repo+":"+tag)
}

func (k *Cluster) Stop(ctx context.Context) error {
	if err := k.kindDeleteCmd(ctx, k.Name).Run(); err != nil {
		return err
	}

	return nil
}

func (k *Cluster) kindDeleteCmd(ctx context.Context, cluster string) *exec.Cmd {
	return exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", cluster)
}

func (k *Cluster) writeKubeconfig(ctx context.Context) error {
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

func (k *Cluster) kindExportKubeconfigCmd(ctx context.Context, cluster, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "kind", "export", "kubeconfig", "--name", cluster)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "KUBECONFIG="+path)

	return cmd
}

type KubectlConfig struct {
	Env        []string
	NoValidate bool
	Timeout    time.Duration
	Namespace  string
}

func (k KubectlConfig) WithTimeout(o time.Duration) KubectlConfig {
	k.Timeout = o
	return k
}

func (k *Cluster) RunKubectlEnsureNS(ctx context.Context, name string, cfg KubectlConfig) error {
	if _, err := k.combinedOutput(k.kubectlCmd(ctx, "get", []string{"ns", name}, cfg)); err != nil {
		if _, err := k.combinedOutput(k.kubectlCmd(ctx, "create", []string{"ns", name}, cfg)); err != nil {
			return err
		}
	}

	return nil
}

func (k *Cluster) Apply(ctx context.Context, path string, cfg KubectlConfig) error {
	if _, err := k.combinedOutput(k.kubectlCmd(ctx, "apply", []string{"-f", path}, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Cluster) WaitUntilDeployAvailable(ctx context.Context, name string, cfg KubectlConfig) error {
	if _, err := k.combinedOutput(k.kubectlCmd(ctx, "wait", []string{"deploy/" + name, "--for=condition=available"}, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Cluster) kubectlCmd(ctx context.Context, c string, args []string, cfg KubectlConfig) *exec.Cmd {
	args = append([]string{c}, args...)

	if cfg.NoValidate {
		args = append(args, "--validate=false")
	}

	if cfg.Namespace != "" {
		args = append(args, "-n="+cfg.Namespace)
	}

	if cfg.Timeout > 0 {
		args = append(args, "--timeout="+fmt.Sprintf("%s", cfg.Timeout))
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, cfg.Env...)

	return cmd
}

type ScriptConfig struct {
	Env []string

	Dir string
}

func (k *Cluster) RunScript(ctx context.Context, path string, cfg ScriptConfig) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	if _, err := k.combinedOutput(k.bashRunScriptCmd(ctx, abs, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Cluster) bashRunScriptCmd(ctx context.Context, path string, cfg ScriptConfig) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", path)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, cfg.Env...)
	cmd.Dir = cfg.Dir

	return cmd
}
