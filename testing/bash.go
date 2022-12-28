package testing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/actions/actions-runner-controller/testing/runtime"
)

type ScriptConfig struct {
	Env []string

	Dir string
}

type Bash struct {
	runtime.Cmdr
}

func (k *Bash) RunScript(ctx context.Context, path string, cfg ScriptConfig) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	if _, err := k.CombinedOutput(k.bashRunScriptCmd(ctx, abs, cfg)); err != nil {
		return err
	}

	return nil
}

func (k *Bash) bashRunScriptCmd(ctx context.Context, path string, cfg ScriptConfig) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "bash", path)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, cfg.Env...)
	cmd.Dir = cfg.Dir

	return cmd
}
