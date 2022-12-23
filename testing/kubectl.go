package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/actions/actions-runner-controller/testing/runtime"
)

type Kubectl struct {
	runtime.Cmdr
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

func (k *Kubectl) EnsureNS(ctx context.Context, name string, cfg KubectlConfig) error {
	if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "get", []string{"ns", name}, cfg)); err != nil {
		if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "create", []string{"ns", name}, cfg)); err != nil {
			return err
		}
	}

	return nil
}

func (k *Kubectl) GetClusterRoleBinding(ctx context.Context, name string, cfg KubectlConfig) (string, error) {
	o, err := k.CombinedOutput(k.kubectlCmd(ctx, "get", []string{"clusterrolebinding", name}, cfg))
	if err != nil {
		return "", err
	}
	return o, nil
}

func (k *Kubectl) CreateClusterRoleBindingServiceAccount(ctx context.Context, name string, clusterrole string, sa string, cfg KubectlConfig) error {
	_, err := k.CombinedOutput(k.kubectlCmd(ctx, "create", []string{"clusterrolebinding", name, "--clusterrole=" + clusterrole, "--serviceaccount=" + sa}, cfg))
	if err != nil {
		return err
	}
	return nil
}

func (k *Kubectl) GetCMLiterals(ctx context.Context, name string, cfg KubectlConfig) (map[string]string, error) {
	o, err := k.CombinedOutput(k.kubectlCmd(ctx, "get", []string{"cm", name, "-o=json"}, cfg))
	if err != nil {
		return nil, err
	}

	var cm struct {
		Data map[string]string `json:"data"`
	}

	if err := json.Unmarshal([]byte(o), &cm); err != nil {
		k.Errorf("Failed unmarshalling this data to JSON:\n%s\n", o)

		return nil, fmt.Errorf("unmarshalling json: %w", err)
	}

	return cm.Data, nil
}

func (k *Kubectl) CreateCMLiterals(ctx context.Context, name string, literals map[string]string, cfg KubectlConfig) error {
	args := []string{"cm", name}

	for k, v := range literals {
		args = append(args, fmt.Sprintf("--from-literal=%s=%s", k, v))
	}

	if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "create", args, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Kubectl) DeleteCM(ctx context.Context, name string, cfg KubectlConfig) error {
	args := []string{"cm", name}

	if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "delete", args, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Kubectl) Apply(ctx context.Context, path string, cfg KubectlConfig) error {
	if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "apply", []string{"-f", path}, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Kubectl) WaitUntilDeployAvailable(ctx context.Context, name string, cfg KubectlConfig) error {
	if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "wait", []string{"deploy/" + name, "--for=condition=available"}, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Kubectl) FindPods(ctx context.Context, label string, cfg KubectlConfig) ([]string, error) {
	args := []string{"po", "-l", label, "-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}`}

	out, err := k.CombinedOutput(k.kubectlCmd(ctx, "get", args, cfg))
	if err != nil {
		return nil, err
	}

	var pods []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			pods = append(pods, l)
		}
	}

	return pods, nil
}

func (k *Kubectl) DeletePods(ctx context.Context, names []string, cfg KubectlConfig) error {
	args := []string{"po"}
	args = append(args, names...)

	if _, err := k.CombinedOutput(k.kubectlCmd(ctx, "delete", args, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Kubectl) kubectlCmd(ctx context.Context, c string, args []string, cfg KubectlConfig) *exec.Cmd {
	args = append([]string{c}, args...)

	if cfg.NoValidate {
		args = append(args, "--validate=false")
	}

	if cfg.Namespace != "" {
		args = append(args, "-n="+cfg.Namespace)
	}

	if cfg.Timeout > 0 {
		args = append(args, fmt.Sprintf("--timeout=%v", cfg.Timeout.String()))
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, cfg.Env...)

	return cmd
}
