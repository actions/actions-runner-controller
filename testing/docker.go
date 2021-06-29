package testing

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/actions-runner-controller/actions-runner-controller/testing/runtime"
)

type Docker struct {
	runtime.Cmdr
}

type DockerBuild struct {
	Dockerfile string
	Args       []BuildArg
	Image      ContainerImage
}

type BuildArg struct {
	Name, Value string
}

func (k *Docker) Build(ctx context.Context, builds []DockerBuild) error {
	for _, build := range builds {
		var args []string
		args = append(args, "--build-arg=TARGETPLATFORM="+"linux/amd64")
		for _, buildArg := range build.Args {
			args = append(args, "--build-arg="+buildArg.Name+"="+buildArg.Value)
		}
		_, err := k.CombinedOutput(k.dockerBuildCmd(ctx, build.Dockerfile, build.Image.Repo, build.Image.Tag, args))

		if err != nil {
			return fmt.Errorf("failed building %v: %w", build, err)
		}
	}

	return nil
}

func (k *Docker) dockerBuildCmd(ctx context.Context, dockerfile, repo, tag string, args []string) *exec.Cmd {
	buildContext := filepath.Dir(dockerfile)
	args = append([]string{"build", "--tag", repo + ":" + tag, "-f", dockerfile, buildContext}, args...)

	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd
}
