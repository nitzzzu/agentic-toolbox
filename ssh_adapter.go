package container

import "github.com/toolbox-tools/toolbox/internal/catalog"

// sshConfigAdapter adapts catalog.SSHConfig to the interface expected by SSHRuntime.
type sshConfigAdapter struct {
	cfg *catalog.SSHConfig
}

func (a *sshConfigAdapter) GetHost() string {
	return a.cfg.Host
}

func (a *sshConfigAdapter) GetIdentity() string {
	return a.cfg.Identity
}

func (a *sshConfigAdapter) GetPort() int {
	if a.cfg.Port == 0 {
		return 22
	}
	return a.cfg.Port
}

// newSSHRuntime creates an SSHRuntime from a catalog SSHConfig.
func newSSHRuntime(cfg *catalog.SSHConfig) *SSHRuntime {
	return &SSHRuntime{cfg: &sshConfigAdapter{cfg: cfg}}
}
