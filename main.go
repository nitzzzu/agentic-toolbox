package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/container"
	"github.com/toolbox-tools/toolbox/internal/env"
	"github.com/toolbox-tools/toolbox/internal/workspace"
)

const version = "0.1.0"

const usage = `toolbox — container-native tool execution for AI agents

Usage:
  toolbox <command> [args]

Core:
  exec [--container <name>] <cmd>   Run a command in the appropriate container
  shell [<container>]               Open an interactive shell

Lifecycle:
  init                              Initialize .toolbox/ in current directory
  up                                Pull images and start all containers
  down                              Stop and remove all containers
  pull                              Pull images without starting
  restart [<container>]             Restart one or all containers
  status                            Show container status

Catalog:
  catalog list                      List containers and their handles
  catalog validate                  Validate catalog.yaml

Environment:
  env list                          Show forwarded environment variables
  env set KEY=VALUE                 Set a workspace env var
  env unset KEY                     Remove a workspace env var

Flags:
  --version                         Print version
  --help                            Print this help
  --workspace <path>                Override workspace root

Environment:
  TOOLBOX_RUNTIME    Override runtime: podman|docker|ssh
  TOOLBOX_WORKSPACE  Override workspace root
`

func main() {
	args := os.Args[1:]

	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		os.Exit(0)
	}

	if args[0] == "--version" || args[0] == "-v" {
		fmt.Println("toolbox version " + version)
		os.Exit(0)
	}

	// Parse global --workspace flag.
	cwd, _ := os.Getwd()
	workspaceOverride := ""
	if idx := indexOf(args, "--workspace"); idx >= 0 && idx+1 < len(args) {
		workspaceOverride = args[idx+1]
		args = append(args[:idx], args[idx+2:]...)
	}
	if v := os.Getenv("TOOLBOX_WORKSPACE"); v != "" {
		workspaceOverride = v
	}

	cmd := args[0]
	rest := args[1:]

	switch cmd {
	case "exec":
		cmdExec(cwd, workspaceOverride, rest)
	case "shell":
		cmdShell(cwd, workspaceOverride, rest)
	case "init":
		cmdInit(cwd, workspaceOverride)
	case "up":
		cmdUp(cwd, workspaceOverride)
	case "down":
		cmdDown(cwd, workspaceOverride)
	case "pull":
		cmdPull(cwd, workspaceOverride)
	case "restart":
		cmdRestart(cwd, workspaceOverride, rest)
	case "status":
		cmdStatus(cwd, workspaceOverride)
	case "catalog":
		cmdCatalog(cwd, workspaceOverride, rest)
	case "env":
		cmdEnv(cwd, workspaceOverride, rest)
	default:
		fmt.Fprintf(os.Stderr, "toolbox: unknown command %q\n\n%s", cmd, usage)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// exec
// ---------------------------------------------------------------------------

func cmdExec(cwd, workspaceOverride string, args []string) {
	// Parse --container flag.
	forceContainer := ""
	if idx := indexOf(args, "--container"); idx >= 0 && idx+1 < len(args) {
		forceContainer = args[idx+1]
		args = append(args[:idx], args[idx+2:]...)
	}

	if len(args) == 0 {
		fatalf("toolbox exec: missing command\nUsage: toolbox exec [--container <name>] <command>")
	}

	command := strings.Join(args, " ")

	mgr := mustManager(cwd, workspaceOverride)
	exitCode, err := mgr.ExecCommand(command, forceContainer)
	if err != nil {
		fatalf("exec: %v", err)
	}
	os.Exit(exitCode)
}

// ---------------------------------------------------------------------------
// shell
// ---------------------------------------------------------------------------

func cmdShell(cwd, workspaceOverride string, args []string) {
	slotName := ""
	if len(args) > 0 {
		slotName = args[0]
	}

	mgr := mustManager(cwd, workspaceOverride)
	if err := mgr.Shell(slotName); err != nil {
		fatalf("shell: %v", err)
	}
}

// ---------------------------------------------------------------------------
// init
// ---------------------------------------------------------------------------

func cmdInit(cwd, workspaceOverride string) {
	root := resolveRoot(cwd, workspaceOverride)
	dotDir := workspace.DotToolbox(root)

	if err := os.MkdirAll(dotDir, 0755); err != nil {
		fatalf("init: %v", err)
	}

	catalogPath := workspace.CatalogPath(root)
	if _, err := os.Stat(catalogPath); err == nil {
		fmt.Println("catalog.yaml already exists — skipping.")
	} else {
		if err := os.WriteFile(catalogPath, []byte(catalog.DefaultCatalog()), 0644); err != nil {
			fatalf("init: write catalog.yaml: %v", err)
		}
		fmt.Println("created .toolbox/catalog.yaml")
	}

	// Create empty env file.
	envPath := workspace.EnvPath(root)
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		content := "# Workspace environment variables (committed to git)\n" +
			"# Format: KEY=VALUE\n" +
			"# These are forwarded to all tool containers based on catalog.yaml env rules.\n"
		if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
			fatalf("init: write env: %v", err)
		}
		fmt.Println("created .toolbox/env")
	}

	// Create env.local placeholder and gitignore it.
	envLocalPath := workspace.EnvLocalPath(root)
	if _, err := os.Stat(envLocalPath); os.IsNotExist(err) {
		content := "# Local secrets — never committed to git\n" +
			"# Format: KEY=VALUE\n" +
			"# OPENAI_API_KEY=sk-...\n" +
			"# DATABASE_URL=postgres://...\n"
		if err := os.WriteFile(envLocalPath, []byte(content), 0600); err != nil {
			fatalf("init: write env.local: %v", err)
		}
		fmt.Println("created .toolbox/env.local")
	}

	// Ensure .gitignore covers env.local.
	ensureGitignore(root)

	fmt.Println("\nDone. Next steps:")
	fmt.Println("  1. Edit .toolbox/catalog.yaml to add your tool containers")
	fmt.Println("  2. Add secrets to .toolbox/env.local")
	fmt.Println("  3. Run `toolbox up` to pull images and start containers")
}

func ensureGitignore(root string) {
	gitignorePath := filepath.Join(root, ".toolbox", ".gitignore")
	entries := ".env.local\nenv.local\n"
	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}
	if !strings.Contains(existing, "env.local") {
		f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(entries)
			f.Close()
		}
	}
}

// ---------------------------------------------------------------------------
// up
// ---------------------------------------------------------------------------

func cmdUp(cwd, workspaceOverride string) {
	mgr := mustManager(cwd, workspaceOverride)
	fmt.Printf("Starting toolbox containers for workspace: %s\n\n", mgr.WorkspaceRoot)
	if err := mgr.Up(); err != nil {
		fatalf("up: %v", err)
	}
	fmt.Println("\nAll containers ready.")
}

// ---------------------------------------------------------------------------
// down
// ---------------------------------------------------------------------------

func cmdDown(cwd, workspaceOverride string) {
	mgr := mustManager(cwd, workspaceOverride)
	fmt.Printf("Stopping toolbox containers for workspace: %s\n\n", mgr.WorkspaceRoot)
	if err := mgr.Down(); err != nil {
		fatalf("down: %v", err)
	}
	fmt.Println("\nDone.")
}

// ---------------------------------------------------------------------------
// pull
// ---------------------------------------------------------------------------

func cmdPull(cwd, workspaceOverride string) {
	mgr := mustManager(cwd, workspaceOverride)
	fmt.Printf("Pulling images for workspace: %s\n\n", mgr.WorkspaceRoot)
	if err := mgr.Pull(); err != nil {
		fatalf("pull: %v", err)
	}
}

// ---------------------------------------------------------------------------
// restart
// ---------------------------------------------------------------------------

func cmdRestart(cwd, workspaceOverride string, args []string) {
	slotName := ""
	if len(args) > 0 {
		slotName = args[0]
	}
	mgr := mustManager(cwd, workspaceOverride)
	if err := mgr.Restart(slotName); err != nil {
		fatalf("restart: %v", err)
	}
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func cmdStatus(cwd, workspaceOverride string) {
	mgr := mustManager(cwd, workspaceOverride)
	statuses, err := mgr.Status()
	if err != nil {
		fatalf("status: %v", err)
	}

	if len(statuses) == 0 {
		fmt.Println("No toolbox containers running.")
		fmt.Println("Run `toolbox up` to start them.")
		return
	}

	fmt.Printf("%-30s %-45s %-20s %s\n", "CONTAINER", "IMAGE", "STATUS", "CREATED")
	fmt.Println(strings.Repeat("-", 110))
	for _, s := range statuses {
		fmt.Printf("%-30s %-45s %-20s %s\n", s.Name, s.Image, s.Status, s.Created)
	}
}

// ---------------------------------------------------------------------------
// catalog
// ---------------------------------------------------------------------------

func cmdCatalog(cwd, workspaceOverride string, args []string) {
	if len(args) == 0 {
		fatalf("toolbox catalog: missing subcommand\nUsage: toolbox catalog list|validate")
	}

	switch args[0] {
	case "list":
		cmdCatalogList(cwd, workspaceOverride)
	case "validate":
		cmdCatalogValidate(cwd, workspaceOverride)
	default:
		fatalf("toolbox catalog: unknown subcommand %q", args[0])
	}
}

func cmdCatalogList(cwd, workspaceOverride string) {
	root := resolveRoot(cwd, workspaceOverride)
	cat, err := catalog.Load(workspace.CatalogPath(root))
	if err != nil {
		fatalf("%v", err)
	}

	fmt.Printf("Catalog: %s\n\n", workspace.CatalogPath(root))
	fmt.Printf("Runtime: %s\n\n", runtimeName(cat))
	fmt.Printf("%-15s %-45s %-10s %s\n", "NAME", "IMAGE", "FALLBACK", "HANDLES")
	fmt.Println(strings.Repeat("-", 100))

	for name, ct := range cat.Containers {
		fallback := ""
		if ct.Fallback {
			fallback = "yes"
		}
		handles := strings.Join(ct.Handles, ", ")
		if handles == "" {
			handles = "(catchall)"
		}
		fmt.Printf("%-15s %-45s %-10s %s\n", name, ct.Image, fallback, handles)
	}
}

func cmdCatalogValidate(cwd, workspaceOverride string) {
	root := resolveRoot(cwd, workspaceOverride)
	cat, err := catalog.Load(workspace.CatalogPath(root))
	if err != nil {
		fatalf("%v", err)
	}

	errs := cat.Validate()
	if len(errs) == 0 {
		fmt.Println("catalog.yaml is valid ✓")
		return
	}

	fmt.Fprintf(os.Stderr, "catalog.yaml has errors:\n")
	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "  • %s\n", e)
	}
	os.Exit(1)
}

func runtimeName(cat *catalog.Catalog) string {
	if cat.Runtime != "" {
		return cat.Runtime
	}
	return "auto-detect"
}

// ---------------------------------------------------------------------------
// env
// ---------------------------------------------------------------------------

func cmdEnv(cwd, workspaceOverride string, args []string) {
	if len(args) == 0 {
		fatalf("toolbox env: missing subcommand\nUsage: toolbox env list|set|unset")
	}

	root := resolveRoot(cwd, workspaceOverride)

	switch args[0] {
	case "list":
		cmdEnvList(root)
	case "set":
		if len(args) < 2 {
			fatalf("toolbox env set: missing KEY=VALUE")
		}
		cmdEnvSet(root, args[1:])
	case "unset":
		if len(args) < 2 {
			fatalf("toolbox env unset: missing KEY")
		}
		cmdEnvUnset(root, args[1])
	default:
		fatalf("toolbox env: unknown subcommand %q", args[0])
	}
}

func cmdEnvList(root string) {
	cat, err := catalog.Load(workspace.CatalogPath(root))
	if err != nil {
		fatalf("%v", err)
	}

	// Show what would be forwarded from host.
	fmt.Println("=== Host env (after forward/deny rules) ===")
	merged, err := env.Merged(root, cat.Env, nil)
	if err != nil {
		fatalf("env: %v", err)
	}
	for _, kv := range merged {
		parts := strings.SplitN(kv, "=", 2)
		// Mask values that look like secrets.
		val := parts[1]
		if isSensitive(parts[0]) {
			val = maskSecret(val)
		}
		fmt.Printf("  %s=%s\n", parts[0], val)
	}

	// Show .toolbox/env.
	fmt.Printf("\n=== .toolbox/env ===\n")
	showEnvFile(workspace.EnvPath(root))

	// Show .toolbox/env.local (masked).
	fmt.Printf("\n=== .toolbox/env.local (secrets masked) ===\n")
	showEnvFileMasked(workspace.EnvLocalPath(root))
}

func showEnvFile(path string) {
	vars, err := env.ParseEnvFile(path)
	if err != nil || len(vars) == 0 {
		fmt.Println("  (empty)")
		return
	}
	for k, v := range vars {
		fmt.Printf("  %s=%s\n", k, v)
	}
}

func showEnvFileMasked(path string) {
	vars, err := env.ParseEnvFile(path)
	if err != nil || len(vars) == 0 {
		fmt.Println("  (empty)")
		return
	}
	for k, v := range vars {
		if isSensitive(k) {
			v = maskSecret(v)
		}
		fmt.Printf("  %s=%s\n", k, v)
	}
}

func cmdEnvSet(root string, assignments []string) {
	vars := make(map[string]string)
	for _, a := range assignments {
		parts := strings.SplitN(a, "=", 2)
		if len(parts) != 2 {
			fatalf("env set: invalid format %q (expected KEY=VALUE)", a)
		}
		vars[parts[0]] = parts[1]
	}

	// Secrets go to env.local, non-secrets to env.
	localVars := make(map[string]string)
	sharedVars := make(map[string]string)
	for k, v := range vars {
		if isSensitive(k) {
			localVars[k] = v
		} else {
			sharedVars[k] = v
		}
	}

	if len(sharedVars) > 0 {
		if err := env.UpdateEnvFile(workspace.EnvPath(root), sharedVars); err != nil {
			fatalf("env set: %v", err)
		}
		for k := range sharedVars {
			fmt.Printf("set %s in .toolbox/env\n", k)
		}
	}

	if len(localVars) > 0 {
		if err := env.UpdateEnvFile(workspace.EnvLocalPath(root), localVars); err != nil {
			fatalf("env set: %v", err)
		}
		for k := range localVars {
			fmt.Printf("set %s in .toolbox/env.local (secret)\n", k)
		}
	}
}

func cmdEnvUnset(root, key string) {
	// Try both files.
	_ = env.DeleteFromEnvFile(workspace.EnvPath(root), key)
	_ = env.DeleteFromEnvFile(workspace.EnvLocalPath(root), key)
	fmt.Printf("unset %s\n", key)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mustManager(cwd, workspaceOverride string) *container.Manager {
	root := resolveRoot(cwd, workspaceOverride)
	cat, err := catalog.Load(workspace.CatalogPath(root))
	if err != nil {
		fatalf("%v", err)
	}

	mgr, err := container.NewManager(root, cat)
	if err != nil {
		fatalf("%v", err)
	}
	return mgr
}

func resolveRoot(cwd, override string) string {
	if override != "" {
		return override
	}
	root, _ := workspace.Root(cwd)
	return root
}

func fatalf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "toolbox: "+format+"\n", args...)
	os.Exit(1)
}

func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}

// isSensitive returns true for env var names that look like secrets.
func isSensitive(key string) bool {
	upper := strings.ToUpper(key)
	for _, kw := range []string{"KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE"} {
		if strings.Contains(upper, kw) {
			return true
		}
	}
	return false
}

func maskSecret(val string) string {
	if len(val) <= 6 {
		return "***"
	}
	return val[:3] + strings.Repeat("*", len(val)-6) + val[len(val)-3:]
}
