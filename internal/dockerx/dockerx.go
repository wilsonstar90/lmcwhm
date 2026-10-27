// Package dockerx drives the Docker Engine through the docker CLI, which the
// installer always provides. (The M3 Docker manager will add the Engine API
// for stats and log streaming.)
package dockerx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrNoDocker = errors.New("docker.not_available")

func Available(ctx context.Context) bool {
	return exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
}

// Run executes `docker args...` and returns trimmed stdout; stderr is folded into the error.
func Run(ctx context.Context, args ...string) (string, error) {
	return RunInput(ctx, nil, args...)
}

func RunInput(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", ErrNoDocker
		}
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("docker %s: %v: %s", strings.Join(args[:min(len(args), 2)], " "), err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// State returns "running", "exited", ... or "" when the container does not exist.
func State(ctx context.Context, name string) string {
	s, err := Run(ctx, "inspect", "-f", "{{.State.Status}}", name)
	if err != nil {
		return ""
	}
	return s
}

func ImageExists(ctx context.Context, ref string) bool {
	_, err := Run(ctx, "image", "inspect", ref)
	return err == nil
}

// Label reads a label from an existing container ("" if absent).
func Label(ctx context.Context, name, key string) string {
	v, _ := Run(ctx, "inspect", "-f", fmt.Sprintf("{{index .Config.Labels %q}}", key), name)
	if v == "<no value>" {
		return ""
	}
	return v
}

func Remove(ctx context.Context, name string) error {
	if State(ctx, name) == "" {
		return nil
	}
	_, err := Run(ctx, "rm", "-f", name)
	return err
}

// Build builds an image from an in-memory Dockerfile (no context directory).
func Build(ctx context.Context, tag, dockerfile string) error {
	_, err := RunInput(ctx, []byte(dockerfile), "build", "-t", tag, "-")
	return err
}

// Logs returns the last n lines of a container's stdout and stderr.
func Logs(ctx context.Context, name string, n int) string {
	out, _ := exec.CommandContext(ctx, "docker", "logs", "--tail", fmt.Sprint(n), name).CombinedOutput()
	return strings.TrimSpace(string(out))
}
