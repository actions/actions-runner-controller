package testing

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/actions-runner-controller/actions-runner-controller/testing/runtime"
)

type Docker struct {
	runtime.Cmdr
}

type DockerBuild struct {
	Dockerfile   string
	Args         []BuildArg
	Image        ContainerImage
	EnableBuildX bool
}

type BuildArg struct {
	Name, Value string
}

func (k *Docker) Build(ctx context.Context, builds []DockerBuild) error {
	for _, build := range builds {
		_, err := k.dockerBuildCombinedOutput(ctx, build)

		if err != nil {
			return fmt.Errorf("failed building %v: %w", build, err)
		}
	}

	return nil
}

func (k *Docker) dockerBuildCombinedOutput(ctx context.Context, build DockerBuild) (string, error) {
	var args []string

	args = append(args, "--build-arg=TARGETPLATFORM="+"linux/amd64")
	for _, buildArg := range build.Args {
		args = append(args, "--build-arg="+buildArg.Name+"="+buildArg.Value)
	}

	dockerfile := build.Dockerfile
	repo := build.Image.Repo
	tag := build.Image.Tag

	buildContext := filepath.Dir(dockerfile)

	docker := "docker"
	env := os.Environ()
	args = append([]string{"build", "--tag", repo + ":" + tag, "-f", dockerfile, buildContext}, args...)

	if build.EnableBuildX {
		args = append([]string{"buildx"}, args...)
		args = append(args, "--load")

		env = append(env, "DOCKER_BUILDKIT=1")
	}

	cmd := exec.CommandContext(ctx, docker, args...)
	cmd.Env = env

	log.Printf("%s %s", docker, strings.Join(args, " "))

	return k.CombinedOutput(cmd)
}

func (k *Docker) Push(ctx context.Context, images []ContainerImage) error {
	for _, img := range images {
		_, err := k.CombinedOutput(dockerPushCmd(ctx, img.Repo, img.Tag))
		if err != nil {
			return err
		}
	}

	return nil
}

func dockerPushCmd(ctx context.Context, repo, tag string) *exec.Cmd {
	return exec.CommandContext(ctx, "docker", "push", repo+":"+tag)
}
