package workspace_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toolbox-tools/toolbox/internal/workspace"
)

// ---------------------------------------------------------------------------
// ContainerName
// ---------------------------------------------------------------------------

func TestContainerName_format(t *testing.T) {
	name := workspace.ContainerName("/home/user/my-project", "base")
	// Must start with "toolbox-"
	if !strings.HasPrefix(name, "toolbox-") {
		t.Errorf("container name should start with toolbox-, got %q", name)
	}
	// Must end with the slot name
	if !strings.HasSuffix(name, "-base") {
		t.Errorf("container name should end with -base, got %q", name)
	}
	// Must contain the sanitized project name
	if !strings.Contains(name, "my-project") {
		t.Errorf("container name should contain project name, got %q", name)
	}
}

func TestContainerName_stable(t *testing.T) {
	// Same inputs always produce the same name.
	a := workspace.ContainerName("/projects/myapp", "base")
	b := workspace.ContainerName("/projects/myapp", "base")
	if a != b {
		t.Errorf("ContainerName is not stable: %q != %q", a, b)
	}
}

func TestContainerName_differentPaths(t *testing.T) {
	// Two projects with the same folder name but different full paths must not collide.
	a := workspace.ContainerName("/alice/myapp", "base")
	b := workspace.ContainerName("/bob/myapp", "base")
	if a == b {
		t.Errorf("different paths should produce different names, both got %q", a)
	}
}

func TestContainerName_differentSlots(t *testing.T) {
	a := workspace.ContainerName("/projects/myapp", "base")
	b := workspace.ContainerName("/projects/myapp", "browser")
	if a == b {
		t.Errorf("different slots should produce different names")
	}
}

func TestContainerName_specialChars(t *testing.T) {
	name := workspace.ContainerName("/home/user/My Project (v2)!", "base")
	// Container names are lowercase alphanumeric + hyphens only.
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-') {
			t.Errorf("container name contains invalid character %q: %q", ch, name)
		}
	}
}

func TestContainerName_onlyAlphanumHyphen(t *testing.T) {
	name := workspace.ContainerName("/path/to/UPPER_CASE", "slot")
	if strings.ContainsAny(name, " \t\n_ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		t.Errorf("container name should be lowercase, got %q", name)
	}
}

// ---------------------------------------------------------------------------
// Paths
// ---------------------------------------------------------------------------

func TestDotToolbox(t *testing.T) {
	got := workspace.DotToolbox("/projects/myapp")
	want := filepath.Join("/projects/myapp", ".toolbox")
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestCatalogPath(t *testing.T) {
	got := workspace.CatalogPath("/projects/myapp")
	want := filepath.Join("/projects/myapp", ".toolbox", "catalog.yaml")
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

// ---------------------------------------------------------------------------
// Root discovery
// ---------------------------------------------------------------------------

func TestRoot_findsToolboxDir(t *testing.T) {
	// Create a nested directory structure with .toolbox at the top.
	root := t.TempDir()
	dotDir := filepath.Join(root, ".toolbox")
	if err := os.MkdirAll(dotDir, 0755); err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	found, err := workspace.Root(subdir)
	if err != nil {
		t.Fatal(err)
	}
	if found != root {
		t.Errorf("want %q, got %q", root, found)
	}
}

func TestRoot_fallbackToCwd(t *testing.T) {
	// When no .toolbox exists, returns the given cwd.
	dir := t.TempDir()
	found, _ := workspace.Root(dir)
	if found != dir {
		t.Errorf("want %q (cwd fallback), got %q", dir, found)
	}
}
