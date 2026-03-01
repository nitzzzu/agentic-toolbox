package container

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// PodmanRuntime implements Runtime using the podman CLI.
type PodmanRuntime struct{}

func (r *PodmanRuntime) Name() string { return "podman" }

func (r *PodmanRuntime) Run(opts RunOpts) error {
	args := []string{
		"run", "-d",
		"--name", opts.Name,
		"-v", opts.WorkspaceRoot + ":/workspace",
		"-w", "/workspace",
	}

	// Rootless UID mapping — files written inside appear owned by host user.
	args = append(args, "--userns=keep-id")

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

	return runInteractive("podman", args...)
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
		"-w", "/workspace",
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
	return runInteractive("docker", args...)
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
	cmd := fmt.Sprintf(
		"podman run -d --name %s -v %s:/workspace -w /workspace --userns=keep-id%s %s sleep infinity",
		opts.Name, opts.WorkspaceRoot, envFlags, opts.Image,
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
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
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

// runInteractive executes a command with full I/O passthrough and returns exit code.
func runInteractive(name string, args ...string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
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
