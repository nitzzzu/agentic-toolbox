package container_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/container"
)

// newTestManager creates a Manager backed by a mockRuntime and a temp workspace.
func newTestManager(t *testing.T, cat *catalog.Catalog, rt *mockRuntime) *container.Manager {
	t.Helper()
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	if err := os.MkdirAll(dotDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create empty env file so env.Merged doesn't error.
	if err := os.WriteFile(filepath.Join(dotDir, "env"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	rt.isRunning = true // container already up, skip lazy start
	return &container.Manager{
		Runtime:       rt,
		WorkspaceRoot: root,
		Catalog:       cat,
	}
}

func simpleCatalog() *catalog.Catalog {
	return &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base": {
				Image:    "base:latest",
				Fallback: true,
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Basic routing
// ---------------------------------------------------------------------------

func TestExecCommand_basic(t *testing.T) {
	rt := &mockRuntime{}
	mgr := newTestManager(t, simpleCatalog(), rt)

	code, err := mgr.ExecCommand(container.ExecOptions{Command: "echo hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("want exit code 0, got %d", code)
	}
	if rt.lastExecOpts.Command != "echo hello" {
		t.Errorf("command not forwarded: got %q", rt.lastExecOpts.Command)
	}
}

func TestExecCommand_forceContainer(t *testing.T) {
	cat := &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base":    {Image: "base:latest", Fallback: true},
			"browser": {Image: "browser:latest", Handles: []string{"playwright"}},
		},
	}
	rt := &mockRuntime{isRunning: true}
	mgr := newTestManager(t, cat, rt)

	_, err := mgr.ExecCommand(container.ExecOptions{
		Command:        "echo hello",
		ForceContainer: "browser",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The exec should go to the browser container name.
	if rt.lastExecOpts.Shell != "sh" {
		t.Errorf("expected shell from browser container, got %q", rt.lastExecOpts.Shell)
	}
}

func TestExecCommand_propagatesExitCode(t *testing.T) {
	rt := &mockRuntime{execCode: 42}
	mgr := newTestManager(t, simpleCatalog(), rt)

	code, err := mgr.ExecCommand(container.ExecOptions{Command: "false"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 42 {
		t.Errorf("want exit code 42, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// Timeout resolution
// ---------------------------------------------------------------------------

func TestExecCommand_timeoutFromCLI(t *testing.T) {
	rt := &mockRuntime{}
	mgr := newTestManager(t, simpleCatalog(), rt)

	_, _ = mgr.ExecCommand(container.ExecOptions{
		Command: "echo",
		Timeout: 5 * time.Second,
	})
	if rt.lastExecOpts.Timeout != 5*time.Second {
		t.Errorf("want timeout 5s, got %v", rt.lastExecOpts.Timeout)
	}
}

func TestExecCommand_timeoutFromContainer(t *testing.T) {
	cat := &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base": {Image: "base:latest", Fallback: true, Timeout: "10s"},
		},
	}
	rt := &mockRuntime{}
	mgr := newTestManager(t, cat, rt)

	_, _ = mgr.ExecCommand(container.ExecOptions{Command: "echo"})
	if rt.lastExecOpts.Timeout != 10*time.Second {
		t.Errorf("want container timeout 10s, got %v", rt.lastExecOpts.Timeout)
	}
}

func TestExecCommand_timeoutFromCatalog(t *testing.T) {
	cat := &catalog.Catalog{
		Timeout: "30s",
		Containers: map[string]catalog.Container{
			"base": {Image: "base:latest", Fallback: true},
		},
	}
	rt := &mockRuntime{}
	mgr := newTestManager(t, cat, rt)

	_, _ = mgr.ExecCommand(container.ExecOptions{Command: "echo"})
	if rt.lastExecOpts.Timeout != 30*time.Second {
		t.Errorf("want catalog timeout 30s, got %v", rt.lastExecOpts.Timeout)
	}
}

func TestExecCommand_timeoutCLIOverridesContainer(t *testing.T) {
	cat := &catalog.Catalog{
		Timeout: "60s",
		Containers: map[string]catalog.Container{
			"base": {Image: "base:latest", Fallback: true, Timeout: "30s"},
		},
	}
	rt := &mockRuntime{}
	mgr := newTestManager(t, cat, rt)

	_, _ = mgr.ExecCommand(container.ExecOptions{
		Command: "echo",
		Timeout: 2 * time.Second, // CLI wins
	})
	if rt.lastExecOpts.Timeout != 2*time.Second {
		t.Errorf("CLI timeout should win; want 2s, got %v", rt.lastExecOpts.Timeout)
	}
}

// ---------------------------------------------------------------------------
// Ephemeral routing
// ---------------------------------------------------------------------------

func TestExecCommand_ephemeral(t *testing.T) {
	cat := &catalog.Catalog{
		Containers: map[string]catalog.Container{
			"base": {
				Image:    "base:latest",
				Fallback: true,
				Network:  "none",
				Limits:   catalog.ResourceLimits{CPU: "1", Memory: "512m", PIDs: 50},
			},
		},
	}
	rt := &mockRuntime{}
	mgr := newTestManager(t, cat, rt)

	code, err := mgr.ExecCommand(container.ExecOptions{
		Command:   "hostname",
		Ephemeral: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != 0 {
		t.Errorf("want exit code 0, got %d", code)
	}
	// Must have called RunEphemeral, not Exec.
	if rt.lastEphemeralOpts.Command != "hostname" {
		t.Errorf("ephemeral command not forwarded: got %q", rt.lastEphemeralOpts.Command)
	}
	if rt.lastEphemeralOpts.Image != "base:latest" {
		t.Errorf("ephemeral image: want %q, got %q", "base:latest", rt.lastEphemeralOpts.Image)
	}
	if rt.lastEphemeralOpts.Network != "none" {
		t.Errorf("ephemeral network: want %q, got %q", "none", rt.lastEphemeralOpts.Network)
	}
	if rt.lastEphemeralOpts.CPULimit != "1" {
		t.Errorf("ephemeral CPULimit: want %q, got %q", "1", rt.lastEphemeralOpts.CPULimit)
	}
	if rt.lastEphemeralOpts.MemLimit != "512m" {
		t.Errorf("ephemeral MemLimit: want %q, got %q", "512m", rt.lastEphemeralOpts.MemLimit)
	}
	if rt.lastEphemeralOpts.PIDsLimit != 50 {
		t.Errorf("ephemeral PIDsLimit: want 50, got %d", rt.lastEphemeralOpts.PIDsLimit)
	}
	// Exec should NOT have been called.
	if rt.lastExecOpts.Command != "" {
		t.Errorf("Exec should not have been called in ephemeral mode, got command %q", rt.lastExecOpts.Command)
	}
}

// ---------------------------------------------------------------------------
// Custom stdout/stderr
// ---------------------------------------------------------------------------

func TestExecCommand_customWriters(t *testing.T) {
	rt := &mockRuntime{}
	mgr := newTestManager(t, simpleCatalog(), rt)

	var out, errBuf bytes.Buffer
	_, _ = mgr.ExecCommand(container.ExecOptions{
		Command: "echo hello",
		Stdout:  &out,
		Stderr:  &errBuf,
	})

	if rt.lastExecOpts.Stdout != &out {
		t.Error("Stdout writer not forwarded to Exec")
	}
	if rt.lastExecOpts.Stderr != &errBuf {
		t.Error("Stderr writer not forwarded to Exec")
	}
}
