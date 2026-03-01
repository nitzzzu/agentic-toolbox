package container

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/env"
	"github.com/toolbox-tools/toolbox/internal/workspace"
)

// ExecOptions are the Manager-level options for ExecCommand.
type ExecOptions struct {
	Command        string
	ForceContainer string
	Timeout        time.Duration // 0 = use catalog default
	Ephemeral      bool          // run in a fresh --rm container
	Stdout         io.Writer     // nil = os.Stdout
	Stderr         io.Writer     // nil = os.Stderr
}

// ExecCommand routes and executes a command through the correct container.
// Returns the exit code of the command.
func (m *Manager) ExecCommand(opts ExecOptions) (int, error) {
	// Resolve which container to use.
	var slotName string
	var ct catalog.Container
	var err error

	if opts.ForceContainer != "" {
		ct, err = m.Catalog.ResolveByName(opts.ForceContainer)
		if err != nil {
			return 1, err
		}
		slotName = opts.ForceContainer
	} else {
		slotName, ct, err = m.Catalog.Resolve(opts.Command)
		if err != nil {
			return 1, err
		}
	}

	// Resolve timeout: CLI > container.Timeout > catalog.Timeout
	timeout := opts.Timeout
	if timeout == 0 && ct.Timeout != "" {
		timeout, _ = time.ParseDuration(ct.Timeout)
	}
	if timeout == 0 && m.Catalog.Timeout != "" {
		timeout, _ = time.ParseDuration(m.Catalog.Timeout)
	}

	// Build the merged env for this exec.
	envVars, err := env.Merged(m.WorkspaceRoot, m.Catalog.Env, ct.Env)
	if err != nil {
		return 1, fmt.Errorf("env: %w", err)
	}

	// Announce the resolved container on stderr so agent UIs can show it.
	fmt.Fprintf(os.Stderr, "[toolbox] container: %s\n", slotName)

	if opts.Ephemeral {
		return m.Runtime.RunEphemeral(EphemeralOpts{
			Image:         ct.Image,
			WorkspaceRoot: m.WorkspaceRoot,
			Command:       opts.Command,
			Env:           envVars,
			Shell:         ct.ShellBin(),
			Network:       ct.Network,
			CPULimit:      ct.Limits.CPU,
			MemLimit:      ct.Limits.Memory,
			PIDsLimit:     ct.Limits.PIDs,
			Timeout:       timeout,
			Stdout:        opts.Stdout,
			Stderr:        opts.Stderr,
		})
	}

	// Ensure the container is running (lazy start).
	if err := m.EnsureRunning(slotName, ct); err != nil {
		return 1, fmt.Errorf("cannot start container %q: %w", slotName, err)
	}

	containerName := workspace.ContainerName(m.WorkspaceRoot, slotName)

	// Execute — streaming I/O directly to agent's stdin/stdout/stderr.
	exitCode, err := m.Runtime.Exec(ExecOpts{
		ContainerName: containerName,
		Command:       opts.Command,
		Env:           envVars,
		Stdin:         true,
		Shell:         ct.ShellBin(),
		Timeout:       timeout,
		Stdout:        opts.Stdout,
		Stderr:        opts.Stderr,
	})
	if err != nil {
		return 1, fmt.Errorf("exec: %w", err)
	}

	return exitCode, nil
}

// Shell opens an interactive shell in the named container.
func (m *Manager) Shell(slotName string) error {
	if slotName == "" {
		// Default to fallback container.
		var ok bool
		slotName, _, ok = m.Catalog.Fallback()
		if !ok {
			return fmt.Errorf("no fallback container defined in catalog")
		}
	}

	ct, err := m.Catalog.ResolveByName(slotName)
	if err != nil {
		return err
	}

	if err := m.EnsureRunning(slotName, ct); err != nil {
		return err
	}

	containerName := workspace.ContainerName(m.WorkspaceRoot, slotName)
	envVars, err := env.Merged(m.WorkspaceRoot, m.Catalog.Env, ct.Env)
	if err != nil {
		return err
	}

	fmt.Printf("Opening shell in %s (%s)...\n", slotName, ct.Image)
	fmt.Printf("Working directory: /workspace\n")
	fmt.Printf("Type 'exit' to return.\n\n")

	_, err = m.Runtime.Exec(ExecOpts{
		ContainerName: containerName,
		Command:       ct.ShellBin(),
		Env:           envVars,
		Stdin:         true,
		Shell:         ct.ShellBin(),
	})
	return err
}
