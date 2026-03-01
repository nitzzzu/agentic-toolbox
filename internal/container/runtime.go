package container

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/toolbox-tools/toolbox/internal/catalog"
)

// Runtime represents a container backend.
type Runtime interface {
	// Name returns "podman", "docker", or "ssh".
	Name() string
	// Run starts a container detached. Returns container ID.
	Run(opts RunOpts) error
	// Exec runs a command in a running container, streaming I/O.
	Exec(opts ExecOpts) (int, error)
	// RunEphemeral runs a command in a fresh container that is removed after exit.
	RunEphemeral(opts EphemeralOpts) (int, error)
	// Stop stops a container.
	Stop(containerName string) error
	// Remove removes a container (must be stopped first or use force).
	Remove(containerName string, force bool) error
	// IsRunning returns true if the container is running.
	IsRunning(containerName string) (bool, error)
	// Pull pulls an image.
	Pull(image string) error
	// Status returns a list of running toolbox containers.
	Status(prefix string) ([]ContainerStatus, error)
}

// RunOpts are options for starting a container.
type RunOpts struct {
	Name          string
	Image         string
	WorkspaceRoot string // host path mounted as /workspace
	Env           []string
	KeepID        bool   // --userns=keep-id (podman only)
	CPULimit      string // e.g. "2", "0.5"
	MemLimit      string // e.g. "512m", "4g"
	PIDsLimit     int
	Network       string // e.g. "none", "host"
}

// ExecOpts are options for execing into a running container.
type ExecOpts struct {
	ContainerName string
	Command       string
	Env           []string
	Stdin         bool
	Shell         string        // shell binary to use, e.g. "sh" or "bash"
	Timeout       time.Duration // 0 = no timeout
	Stdout        io.Writer     // nil = os.Stdout
	Stderr        io.Writer     // nil = os.Stderr
}

// EphemeralOpts are options for running a one-shot container (--rm).
type EphemeralOpts struct {
	Image         string
	WorkspaceRoot string
	Command       string
	Env           []string
	Shell         string
	Network       string
	CPULimit      string
	MemLimit      string
	PIDsLimit     int
	Timeout       time.Duration
	Stdout        io.Writer // nil = os.Stdout
	Stderr        io.Writer // nil = os.Stderr
}

// ContainerStatus is returned by Status().
type ContainerStatus struct {
	Name    string
	Image   string
	Status  string
	Created string
}

// Detect auto-detects the available runtime based on catalog config and environment.
func Detect(cat *catalog.Catalog) (Runtime, error) {
	// Explicit override via env var.
	if rt := runtimeEnvVar(); rt != "" {
		return runtimeFromName(rt, cat)
	}

	// Explicit in catalog.
	if cat != nil && cat.Runtime != "" {
		return runtimeFromName(cat.Runtime, cat)
	}

	// Auto-detect: prefer podman.
	if commandExists("podman") {
		return &PodmanRuntime{}, nil
	}
	if commandExists("docker") {
		return &DockerRuntime{}, nil
	}

	return nil, fmt.Errorf(
		"no container runtime found.\n" +
			"Install Podman:  https://podman.io/getting-started/installation\n" +
			"Install Docker:  https://docs.docker.com/engine/install/\n" +
			"Or set TOOLBOX_RUNTIME=podman|docker|ssh",
	)
}

func runtimeFromName(name string, cat *catalog.Catalog) (Runtime, error) {
	switch strings.ToLower(name) {
	case "podman":
		return &PodmanRuntime{}, nil
	case "docker":
		return &DockerRuntime{}, nil
	case "ssh":
		if cat == nil || cat.SSH == nil {
			return nil, fmt.Errorf("runtime=ssh requires [ssh] config in catalog.yaml")
		}
		return newSSHRuntime(cat.SSH), nil
	default:
		return nil, fmt.Errorf("unknown runtime %q — must be podman, docker, or ssh", name)
	}
}

func runtimeEnvVar() string {
	// TOOLBOX_RUNTIME env var takes priority.
	return strings.TrimSpace(getenv("TOOLBOX_RUNTIME"))
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
