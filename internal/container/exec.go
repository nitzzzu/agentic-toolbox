package container

import (
	"fmt"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/env"
	"github.com/toolbox-tools/toolbox/internal/workspace"
)

// ExecCommand routes and executes a command through the correct container.
// Returns the exit code of the command.
func (m *Manager) ExecCommand(command string, forceContainer string) (int, error) {
	// Resolve which container to use.
	var slotName string
	var ct catalog.Container
	var err error

	if forceContainer != "" {
		ct, err = m.Catalog.ResolveByName(forceContainer)
		if err != nil {
			return 1, err
		}
		slotName = forceContainer
	} else {
		slotName, ct, err = m.Catalog.Resolve(command)
		if err != nil {
			return 1, err
		}
	}

	// Ensure the container is running (lazy start).
	if err := m.EnsureRunning(slotName, ct); err != nil {
		return 1, fmt.Errorf("cannot start container %q: %w", slotName, err)
	}

	containerName := workspace.ContainerName(m.WorkspaceRoot, slotName)

	// Build the merged env for this exec.
	envVars, err := env.Merged(m.WorkspaceRoot, m.Catalog.Env, ct.Env)
	if err != nil {
		return 1, fmt.Errorf("env: %w", err)
	}

	// Execute — streaming I/O directly to agent's stdin/stdout/stderr.
	exitCode, err := m.Runtime.Exec(ExecOpts{
		ContainerName: containerName,
		Command:       command,
		Env:           envVars,
		Stdin:         true,
		Shell:         ct.ShellBin(),
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
