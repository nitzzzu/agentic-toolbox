package container

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// PodmanRuntime implements Runtime using the podman CLI.
type PodmanRuntime struct{}

func (r *PodmanRuntime) Name() string { return "podman" }

func (r *PodmanRuntime) Run(opts RunOpts) error {
	args := []string{
		"run", "-d",
		"--name", opts.Name,
		"-v", opts.WorkspaceRoot + ":/workspace",
		"-v", "/workspace/.toolbox", // shadow .toolbox with an anonymous volume
		"-w", "/workspace",
	}

	// Rootless UID mapping — files written inside appear owned by host user.
	args = append(args, "--userns=keep-id")

	// Resource limits.
	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemLimit != "" {
		args = append(args, "--memory", opts.MemLimit)
	}
	if opts.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.PIDsLimit))
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}

	// Forward env vars.
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}

	// Keep container alive.
	args = append(args, opts.Image, "sleep", "infinity")

	return run("podman", args...)
}

func (r *PodmanRuntime) Exec(opts ExecOpts) (int, error) {
	args := []string{"exec"}

	// Forward per-exec env vars.
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}

	if opts.Stdin {
		args = append(args, "-i")
	}

	args = append(args, opts.ContainerName, opts.Shell, "-c", opts.Command)

	return execWithOpts(opts.Timeout, opts.Stdout, opts.Stderr, "podman", args...)
}

func (r *PodmanRuntime) RunEphemeral(opts EphemeralOpts) (int, error) {
	args := []string{"run", "--rm",
		"-v", opts.WorkspaceRoot + ":/workspace",
		"-v", "/workspace/.toolbox",
		"-w", "/workspace",
		"--userns=keep-id",
	}
	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemLimit != "" {
		args = append(args, "--memory", opts.MemLimit)
	}
	if opts.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.PIDsLimit))
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}
	shell := opts.Shell
	if shell == "" {
		shell = "sh"
	}
	args = append(args, opts.Image, shell, "-c", opts.Command)
	return execWithOpts(opts.Timeout, opts.Stdout, opts.Stderr, "podman", args...)
}

func (r *PodmanRuntime) Stop(name string) error {
	return run("podman", "stop", name)
}

func (r *PodmanRuntime) Remove(name string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, name)
	return run("podman", args...)
}

func (r *PodmanRuntime) IsRunning(name string) (bool, error) {
	out, err := output("podman", "inspect", "--format", "{{.State.Running}}", name)
	if err != nil {
		// Container doesn't exist.
		return false, nil
	}
	return strings.TrimSpace(out) == "true", nil
}

func (r *PodmanRuntime) Pull(image string) error {
	return runInteractiveSimple("podman", "pull", image)
}

func (r *PodmanRuntime) Status(prefix string) ([]ContainerStatus, error) {
	out, err := output("podman", "ps", "-a",
		"--format", "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Created}}",
		"--filter", "name="+prefix,
	)
	if err != nil {
		return nil, err
	}
	return parseStatusOutput(out), nil
}

// DockerRuntime implements Runtime using the docker CLI.
type DockerRuntime struct{}

func (r *DockerRuntime) Name() string { return "docker" }

func (r *DockerRuntime) Run(opts RunOpts) error {
	args := []string{
		"run", "-d",
		"--name", opts.Name,
		"-v", opts.WorkspaceRoot + ":/workspace",
		"-v", "/workspace/.toolbox", // shadow .toolbox with an anonymous volume
		"-w", "/workspace",
	}

	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemLimit != "" {
		args = append(args, "--memory", opts.MemLimit)
	}
	if opts.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.PIDsLimit))
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}

	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}

	args = append(args, opts.Image, "sleep", "infinity")
	return run("docker", args...)
}

func (r *DockerRuntime) Exec(opts ExecOpts) (int, error) {
	args := []string{"exec"}
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}
	if opts.Stdin {
		args = append(args, "-i")
	}
	args = append(args, opts.ContainerName, opts.Shell, "-c", opts.Command)
	return execWithOpts(opts.Timeout, opts.Stdout, opts.Stderr, "docker", args...)
}

func (r *DockerRuntime) RunEphemeral(opts EphemeralOpts) (int, error) {
	args := []string{"run", "--rm",
		"-v", opts.WorkspaceRoot + ":/workspace",
		"-v", "/workspace/.toolbox",
		"-w", "/workspace",
	}
	if opts.CPULimit != "" {
		args = append(args, "--cpus", opts.CPULimit)
	}
	if opts.MemLimit != "" {
		args = append(args, "--memory", opts.MemLimit)
	}
	if opts.PIDsLimit > 0 {
		args = append(args, "--pids-limit", strconv.Itoa(opts.PIDsLimit))
	}
	if opts.Network != "" {
		args = append(args, "--network", opts.Network)
	}
	for _, kv := range opts.Env {
		args = append(args, "--env", kv)
	}
	shell := opts.Shell
	if shell == "" {
		shell = "sh"
	}
	args = append(args, opts.Image, shell, "-c", opts.Command)
	return execWithOpts(opts.Timeout, opts.Stdout, opts.Stderr, "docker", args...)
}

func (r *DockerRuntime) Stop(name string) error {
	return run("docker", "stop", name)
}

func (r *DockerRuntime) Remove(name string, force bool) error {
	args := []string{"rm"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, name)
	return run("docker", args...)
}

func (r *DockerRuntime) IsRunning(name string) (bool, error) {
	out, err := output("docker", "inspect", "--format", "{{.State.Running}}", name)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) == "true", nil
}

func (r *DockerRuntime) Pull(image string) error {
	return runInteractiveSimple("docker", "pull", image)
}

func (r *DockerRuntime) Status(prefix string) ([]ContainerStatus, error) {
	out, err := output("docker", "ps", "-a",
		"--format", "{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.CreatedAt}}",
		"--filter", "name="+prefix,
	)
	if err != nil {
		return nil, err
	}
	return parseStatusOutput(out), nil
}

// SSHRuntime implements Runtime by SSHing to a remote host and running podman there.
type SSHRuntime struct {
	cfg interface {
		GetHost() string
		GetIdentity() string
		GetPort() int
	}
}

func (r *SSHRuntime) Name() string { return "ssh" }

func (r *SSHRuntime) ssh(remoteCmd string) *exec.Cmd {
	args := []string{}
	if id := r.cfg.GetIdentity(); id != "" {
		args = append(args, "-i", id)
	}
	if port := r.cfg.GetPort(); port != 0 {
		args = append(args, "-p", fmt.Sprintf("%d", port))
	}
	args = append(args, "-o", "StrictHostKeyChecking=accept-new")
	args = append(args, r.cfg.GetHost(), remoteCmd)
	return exec.Command("ssh", args...)
}

func (r *SSHRuntime) Run(opts RunOpts) error {
	// Build the remote podman run command.
	envFlags := ""
	for _, kv := range opts.Env {
		envFlags += fmt.Sprintf(" --env %q", kv)
	}
	limitFlags := ""
	if opts.CPULimit != "" {
		limitFlags += " --cpus " + opts.CPULimit
	}
	if opts.MemLimit != "" {
		limitFlags += " --memory " + opts.MemLimit
	}
	if opts.PIDsLimit > 0 {
		limitFlags += " --pids-limit " + strconv.Itoa(opts.PIDsLimit)
	}
	if opts.Network != "" {
		limitFlags += " --network " + opts.Network
	}
	cmd := fmt.Sprintf(
		"podman run -d --name %s -v %s:/workspace -v /workspace/.toolbox -w /workspace --userns=keep-id%s%s %s sleep infinity",
		opts.Name, opts.WorkspaceRoot, limitFlags, envFlags, opts.Image,
	)
	c := r.ssh(cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func (r *SSHRuntime) Exec(opts ExecOpts) (int, error) {
	envFlags := ""
	for _, kv := range opts.Env {
		envFlags += fmt.Sprintf(" --env %q", kv)
	}
	cmd := fmt.Sprintf(
		"podman exec%s %s %s -c %q",
		envFlags, opts.ContainerName, opts.Shell, opts.Command,
	)
	c := r.ssh(cmd)
	c.Stdin = os.Stdin
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	c.Stdout = stdout
	c.Stderr = stderr
	if opts.Timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		defer cancel()
		done := make(chan error, 1)
		if err := c.Start(); err != nil {
			return 1, err
		}
		go func() { done <- c.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return exitErr.ExitCode(), nil
				}
				return 1, err
			}
			return 0, nil
		case <-ctx.Done():
			_ = c.Process.Kill()
			return 1, fmt.Errorf("exec timed out after %s", opts.Timeout)
		}
	}
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func (r *SSHRuntime) RunEphemeral(opts EphemeralOpts) (int, error) {
	envFlags := ""
	for _, kv := range opts.Env {
		envFlags += fmt.Sprintf(" --env %q", kv)
	}
	limitFlags := ""
	if opts.CPULimit != "" {
		limitFlags += " --cpus " + opts.CPULimit
	}
	if opts.MemLimit != "" {
		limitFlags += " --memory " + opts.MemLimit
	}
	if opts.PIDsLimit > 0 {
		limitFlags += " --pids-limit " + strconv.Itoa(opts.PIDsLimit)
	}
	if opts.Network != "" {
		limitFlags += " --network " + opts.Network
	}
	shell := opts.Shell
	if shell == "" {
		shell = "sh"
	}
	cmd := fmt.Sprintf(
		"podman run --rm -v %s:/workspace -v /workspace/.toolbox -w /workspace --userns=keep-id%s%s %s %s -c %q",
		opts.WorkspaceRoot, limitFlags, envFlags, opts.Image, shell, opts.Command,
	)
	c := r.ssh(cmd)
	c.Stdin = os.Stdin
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	c.Stdout = stdout
	c.Stderr = stderr
	if opts.Timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		defer cancel()
		done := make(chan error, 1)
		if err := c.Start(); err != nil {
			return 1, err
		}
		go func() { done <- c.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return exitErr.ExitCode(), nil
				}
				return 1, err
			}
			return 0, nil
		case <-ctx.Done():
			_ = c.Process.Kill()
			return 1, fmt.Errorf("exec timed out after %s", opts.Timeout)
		}
	}
	if err := c.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

func (r *SSHRuntime) Stop(name string) error {
	return r.ssh("podman stop " + name).Run()
}

func (r *SSHRuntime) Remove(name string, force bool) error {
	cmd := "podman rm "
	if force {
		cmd += "--force "
	}
	cmd += name
	return r.ssh(cmd).Run()
}

func (r *SSHRuntime) IsRunning(name string) (bool, error) {
	c := r.ssh("podman inspect --format '{{.State.Running}}' " + name)
	out, err := c.Output()
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(out)) == "true", nil
}

func (r *SSHRuntime) Pull(image string) error {
	c := r.ssh("podman pull " + image)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func (r *SSHRuntime) Status(prefix string) ([]ContainerStatus, error) {
	c := r.ssh(fmt.Sprintf(
		"podman ps -a --format '{{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Created}}' --filter name=%s",
		prefix,
	))
	out, err := c.Output()
	if err != nil {
		return nil, err
	}
	return parseStatusOutput(string(out)), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// run executes a command with stdout/stderr suppressed (for lifecycle ops).
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// execWithOpts executes a command with optional timeout and custom writers,
// returning the exit code. stdout/stderr default to os.Stdout/os.Stderr when nil.
func execWithOpts(timeout time.Duration, stdout, stderr io.Writer, name string, args ...string) (int, error) {
	var cmd *exec.Cmd
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd = exec.CommandContext(ctx, name, args...)
	} else {
		cmd = exec.Command(name, args...)
	}
	cmd.Stdin = os.Stdin
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = os.Stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	} else {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		// Check for timeout before exit code — context error takes priority.
		if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return 1, fmt.Errorf("exec timed out after %s", timeout)
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// runInteractive executes a command with full I/O passthrough and returns exit code.
func runInteractive(name string, args ...string) (int, error) {
	return execWithOpts(0, nil, nil, name, args...)
}

// runInteractiveSimple runs with I/O passthrough and returns an error on non-zero exit.
func runInteractiveSimple(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// output executes a command and returns stdout as a string.
func output(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	return string(out), err
}

func parseStatusOutput(out string) []ContainerStatus {
	var result []ContainerStatus
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		result = append(result, ContainerStatus{
			Name:    parts[0],
			Image:   parts[1],
			Status:  parts[2],
			Created: parts[3],
		})
	}
	return result
}

func getenv(key string) string {
	return os.Getenv(key)
}
