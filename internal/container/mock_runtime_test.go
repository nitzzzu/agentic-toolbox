package container_test

import "github.com/toolbox-tools/toolbox/internal/container"

// mockRuntime is a test double that captures calls without touching real containers.
type mockRuntime struct {
	lastExecOpts      container.ExecOpts
	lastEphemeralOpts container.EphemeralOpts
	lastRunOpts       container.RunOpts

	execCode      int
	execErr       error
	ephemeralCode int
	ephemeralErr  error
	isRunning     bool
}

func (m *mockRuntime) Name() string { return "mock" }

func (m *mockRuntime) Run(opts container.RunOpts) error {
	m.lastRunOpts = opts
	return nil
}

func (m *mockRuntime) Exec(opts container.ExecOpts) (int, error) {
	m.lastExecOpts = opts
	return m.execCode, m.execErr
}

func (m *mockRuntime) RunEphemeral(opts container.EphemeralOpts) (int, error) {
	m.lastEphemeralOpts = opts
	return m.ephemeralCode, m.ephemeralErr
}

func (m *mockRuntime) Stop(name string) error           { return nil }
func (m *mockRuntime) Remove(name string, _ bool) error { return nil }
func (m *mockRuntime) IsRunning(name string) (bool, error) {
	return m.isRunning, nil
}
func (m *mockRuntime) Pull(image string) error { return nil }
func (m *mockRuntime) Status(prefix string) ([]container.ContainerStatus, error) {
	return []container.ContainerStatus{
		{Name: "toolbox-test-base", Image: "base:latest", Status: "Up"},
	}, nil
}
