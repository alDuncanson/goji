package util

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// CmdResult captures command execution output.
type CmdResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func RunCommand(ctx context.Context, cwd string, env []string, name string, args ...string) (CmdResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	if len(env) > 0 {
		cmd.Env = append(cmd.Env, env...)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := CmdResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil {
		return result, fmt.Errorf("command failed: %w", err)
	}

	return result, nil
}
