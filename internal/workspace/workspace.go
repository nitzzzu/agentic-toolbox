package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Root finds the workspace root by walking up from cwd looking for .toolbox/
// If not found, returns cwd itself (user may be running toolbox init).
func Root(cwd string) (string, error) {
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".toolbox")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root — return original cwd
			return cwd, nil
		}
		dir = parent
	}
}

// ContainerName returns a stable, unique container name for a given
// workspace + container slot. Format: toolbox-{workspace}-{slot}
// The workspace name is sanitized: lowercase, alphanumeric+dash only.
func ContainerName(workspaceRoot, slot string) string {
	name := filepath.Base(workspaceRoot)
	name = sanitize(name)
	slot = sanitize(slot)
	return fmt.Sprintf("toolbox-%s-%s", name, slot)
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func sanitize(s string) string {
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "x"
	}
	return s
}

// DotToolbox returns the .toolbox directory path for the given workspace root.
func DotToolbox(root string) string {
	return filepath.Join(root, ".toolbox")
}

// CatalogPath returns the path to catalog.yaml.
func CatalogPath(root string) string {
	return filepath.Join(root, ".toolbox", "catalog.yaml")
}

// EnvPath returns the path to .toolbox/env.
func EnvPath(root string) string {
	return filepath.Join(root, ".toolbox", "env")
}

// EnvLocalPath returns the path to .toolbox/env.local.
func EnvLocalPath(root string) string {
	return filepath.Join(root, ".toolbox", "env.local")
}
