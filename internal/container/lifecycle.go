package container

import (
	"fmt"
	"strings"
	"time"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/env"
	"github.com/toolbox-tools/toolbox/internal/workspace"
)

// Manager handles container lifecycle for a workspace.
type Manager struct {
	Runtime       Runtime
	WorkspaceRoot string
	Catalog       *catalog.Catalog
}

// NewManager creates a Manager with auto-detected runtime.
func NewManager(workspaceRoot string, cat *catalog.Catalog) (*Manager, error) {
	rt, err := Detect(cat)
	if err != nil {
		return nil, err
	}
	return &Manager{
		Runtime:       rt,
		WorkspaceRoot: workspaceRoot,
		Catalog:       cat,
	}, nil
}

// Up pulls images and starts all containers defined in the catalog.
func (m *Manager) Up() error {
	for name, ct := range m.Catalog.Containers {
		fmt.Printf("→ %s (%s)\n", name, ct.Image)

		fmt.Printf("  pulling image...\n")
		if err := m.Runtime.Pull(ct.Image); err != nil {
			return fmt.Errorf("pull %s: %w", ct.Image, err)
		}

		containerName := workspace.ContainerName(m.WorkspaceRoot, name)
		running, err := m.Runtime.IsRunning(containerName)
		if err != nil {
			return err
		}
		if running {
			fmt.Printf("  already running ✓\n")
			continue
		}

		fmt.Printf("  starting...\n")
		if err := m.Start(name, ct); err != nil {
			return fmt.Errorf("start %s: %w", name, err)
		}
		fmt.Printf("  ready ✓\n")
	}
	return nil
}

// Down stops and removes all toolbox containers for this workspace.
func (m *Manager) Down() error {
	statuses, err := m.Runtime.Status("toolbox-")
	if err != nil {
		return err
	}

	// Build the workspace-specific prefix to filter only this workspace's containers.
	// ContainerName("base") → "toolbox-myproject-base", so prefix is "toolbox-myproject-"
	sampleName := workspace.ContainerName(m.WorkspaceRoot, "x")
	// sampleName = "toolbox-myproject-x", strip the trailing "-x"
	wsPrefix := sampleName[:len(sampleName)-2] + "-"

	for _, s := range statuses {
		if !strings.HasPrefix(s.Name, wsPrefix) {
			continue
		}
		fmt.Printf("→ stopping %s\n", s.Name)
		_ = m.Runtime.Stop(s.Name)
		if err := m.Runtime.Remove(s.Name, true); err != nil {
			fmt.Printf("  warning: remove %s: %v\n", s.Name, err)
		}
	}
	return nil
}

// Start starts a single container by slot name and definition.
func (m *Manager) Start(slotName string, ct catalog.Container) error {
	containerName := workspace.ContainerName(m.WorkspaceRoot, slotName)

	// Build env for this container.
	envVars, err := env.Merged(m.WorkspaceRoot, m.Catalog.Env, ct.Env)
	if err != nil {
		return fmt.Errorf("env: %w", err)
	}

	return m.Runtime.Run(RunOpts{
		Name:          containerName,
		Image:         ct.Image,
		WorkspaceRoot: m.WorkspaceRoot,
		Env:           envVars,
		KeepID:        true,
	})
}

// EnsureRunning starts the container if it's not already running.
// This is the lazy-start path called by exec.
func (m *Manager) EnsureRunning(slotName string, ct catalog.Container) error {
	containerName := workspace.ContainerName(m.WorkspaceRoot, slotName)

	running, err := m.Runtime.IsRunning(containerName)
	if err != nil {
		return err
	}
	if running {
		return nil
	}

	fmt.Printf("[toolbox] starting %s container (%s)...\n", slotName, ct.Image)

	// Pull image first in case it's not local. If pull fails (e.g. no registry
	// access) warn and fall through — Start will succeed if the image is cached locally.
	if err := m.Runtime.Pull(ct.Image); err != nil {
		fmt.Printf("[toolbox] warning: could not pull %s: %v (using local cache)\n", ct.Image, err)
	}

	if err := m.Start(slotName, ct); err != nil {
		return err
	}

	// Wait for container to be ready (max 30s).
	return m.waitReady(containerName, 30*time.Second)
}

// waitReady polls until the container responds to exec.
func (m *Manager) waitReady(containerName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, err := m.Runtime.IsRunning(containerName)
		if err == nil && running {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("container %s did not become ready within %s", containerName, timeout)
}

// Restart stops and restarts one or all containers.
func (m *Manager) Restart(slotName string) error {
	if slotName == "" {
		// Restart all.
		for name, ct := range m.Catalog.Containers {
			if err := m.restartOne(name, ct); err != nil {
				return err
			}
		}
		return nil
	}
	ct, err := m.Catalog.ResolveByName(slotName)
	if err != nil {
		return err
	}
	return m.restartOne(slotName, ct)
}

func (m *Manager) restartOne(slotName string, ct catalog.Container) error {
	containerName := workspace.ContainerName(m.WorkspaceRoot, slotName)
	fmt.Printf("→ restarting %s\n", containerName)
	_ = m.Runtime.Stop(containerName)
	_ = m.Runtime.Remove(containerName, true)
	return m.Start(slotName, ct)
}

// Pull pulls all images in the catalog without starting them.
func (m *Manager) Pull() error {
	for name, ct := range m.Catalog.Containers {
		fmt.Printf("→ pulling %s (%s)\n", name, ct.Image)
		if err := m.Runtime.Pull(ct.Image); err != nil {
			return fmt.Errorf("pull %s: %w", ct.Image, err)
		}
	}
	return nil
}

// Status returns the running state of all toolbox containers for this workspace.
func (m *Manager) Status() ([]ContainerStatus, error) {
	sampleName := workspace.ContainerName(m.WorkspaceRoot, "x")
	wsPrefix := sampleName[:len(sampleName)-2] + "-"
	return m.Runtime.Status(wsPrefix)
}
