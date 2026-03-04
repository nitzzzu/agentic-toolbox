package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/toolbox-tools/toolbox/internal/catalog"
	"github.com/toolbox-tools/toolbox/internal/container"
	"github.com/toolbox-tools/toolbox/internal/env"
	"github.com/toolbox-tools/toolbox/internal/fetch"
	execlog "github.com/toolbox-tools/toolbox/internal/log"
	"github.com/toolbox-tools/toolbox/internal/serve"
	"github.com/toolbox-tools/toolbox/internal/workspace"
)

const version = "0.1.0"

const usage = `toolbox — container-native tool execution for AI agents

Usage:
  toolbox <command> [args]

Core:
  exec [--container <name>] [--timeout <dur>] [--ephemeral] <cmd>
                                    Run a command in the appropriate container
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

Read:
  read [--lines N-M] [--grep <pat>] [--toc] <url-or-file>
                                    Read a URL or local file; URLs are fetched and
                                    converted to markdown automatically

Observability:
  log [--tail N] [--json]           Show exec history (default tail: 50)
  serve [--port N]                  Start HTTP API server (default port: 7070)

Exec flags:
  --container <name>                Force a specific container
  --timeout <duration>              Timeout, e.g. 30s, 2m (overrides catalog)
  --ephemeral                       Run in a fresh container (no persistent state)

Read flags:
  --lines N-M                       Return lines N through M
  --grep <pattern>                  Search with a Go regexp
  --ignore-case                     Case-insensitive grep
  --literal                         Treat grep pattern as literal string
  --context N                       Lines of context around each match (default 2)
  --limit N                         Max grep matches (default 50)
  --toc                             Metadata + table of contents (markdown only)

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
	case "log":
		cmdLog(cwd, workspaceOverride, rest)
	case "serve":
		cmdServe(cwd, workspaceOverride, rest)
	case "fetch":
		cmdRead(cwd, workspaceOverride, rest)
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

	// Parse --timeout flag.
	var timeout time.Duration
	if idx := indexOf(args, "--timeout"); idx >= 0 && idx+1 < len(args) {
		timeout, _ = time.ParseDuration(args[idx+1])
		args = append(args[:idx], args[idx+2:]...)
	}

	// Parse --ephemeral flag.
	ephemeral := false
	if idx := indexOf(args, "--ephemeral"); idx >= 0 {
		ephemeral = true
		args = append(args[:idx], args[idx+1:]...)
	}

	if len(args) == 0 {
		fatalf("toolbox exec: missing command\nUsage: toolbox exec [--container <name>] [--timeout <dur>] [--ephemeral] <command>")
	}

	command := strings.Join(args, " ")

	mgr := mustManager(cwd, workspaceOverride)

	start := time.Now()
	exitCode, err := mgr.ExecCommand(container.ExecOptions{
		Command:        command,
		ForceContainer: forceContainer,
		Timeout:        timeout,
		Ephemeral:      ephemeral,
	})
	elapsed := time.Since(start)

	// Append to exec log (best-effort, don't fail on log errors).
	_ = execlog.Append(mgr.WorkspaceRoot, execlog.Entry{
		TS:        start,
		Container: forceContainer,
		Command:   command,
		Ephemeral: ephemeral,
		ExitCode:  exitCode,
		Ms:        elapsed.Milliseconds(),
	})

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
	existing := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existing = string(data)
	}

	var missing []string
	for _, entry := range []string{"env.local", "exec.log", "fetch-cache/"} {
		if !strings.Contains(existing, entry) {
			missing = append(missing, entry)
		}
	}
	if len(missing) == 0 {
		return
	}

	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		for _, entry := range missing {
			f.WriteString(entry + "\n")
		}
		f.Close()
	}
}

// buildFetchConfig converts a catalog FetchConfig into a fetch.Config.
// cat may be nil (no catalog found); in that case defaults are used.
func buildFetchConfig(root string, cat *catalog.Catalog) fetch.Config {
	if cat == nil {
		return fetch.Config{}
	}
	cfg := fetch.Config{
		StripSelectors: cat.Fetch.StripSelectors,
		AllowedDomains: cat.Fetch.AllowedDomains,
	}
	if cat.Fetch.CacheTTL != "" {
		if d, err := time.ParseDuration(cat.Fetch.CacheTTL); err == nil {
			cfg.CacheTTL = d
		}
	}
	if len(cat.Fetch.Domains) > 0 {
		cfg.Domains = make(map[string]fetch.DomainConfig, len(cat.Fetch.Domains))
		for domain, dc := range cat.Fetch.Domains {
			cfg.Domains[domain] = fetch.DomainConfig{
				StripSelectors: dc.StripSelectors,
				ProxyURL:       dc.ProxyURL,
			}
		}
	}
	_ = root
	return cfg
}

// parseLineRange parses "N-M" into start and end integers.
func parseLineRange(s string, start, end *int) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) == 2 {
		fmt.Sscanf(parts[0], "%d", start)
		fmt.Sscanf(parts[1], "%d", end)
	} else {
		fmt.Sscanf(s, "%d", start)
		*end = *start
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
// log
// ---------------------------------------------------------------------------

func cmdLog(cwd, workspaceOverride string, args []string) {
	tail := 50
	jsonOut := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--tail":
			if i+1 < len(args) {
				fmt.Sscanf(args[i+1], "%d", &tail)
				i++
			}
		case "--json":
			jsonOut = true
		}
	}

	root := resolveRoot(cwd, workspaceOverride)
	entries, err := execlog.Read(root, tail)
	if err != nil {
		fatalf("log: %v", err)
	}
	if len(entries) == 0 {
		fmt.Println("No exec log entries.")
		return
	}

	if jsonOut {
		for _, e := range entries {
			line, _ := jsonMarshal(e)
			fmt.Println(string(line))
		}
		return
	}

	// Human-readable format.
	fmt.Printf("%-20s  %-15s  %-40s  %-6s  %s\n", "TIME", "CONTAINER", "COMMAND", "EXIT", "DURATION")
	fmt.Println(strings.Repeat("-", 100))
	for _, e := range entries {
		ts := e.TS.Format("2006-01-02 15:04:05")
		dur := fmt.Sprintf("%.1fs", float64(e.Ms)/1000)
		cmd := e.Command
		if len(cmd) > 40 {
			cmd = cmd[:37] + "..."
		}
		ct := e.Container
		if ct == "" {
			ct = "(auto)"
		}
		fmt.Printf("%-20s  %-15s  %-40s  %-6d  %s\n", ts, ct, cmd, e.ExitCode, dur)
	}
}

// ---------------------------------------------------------------------------
// serve
// ---------------------------------------------------------------------------

func cmdServe(cwd, workspaceOverride string, args []string) {
	port := 7070
	for i := 0; i < len(args); i++ {
		if args[i] == "--port" && i+1 < len(args) {
			fmt.Sscanf(args[i+1], "%d", &port)
			i++
		}
	}

	root := resolveRoot(cwd, workspaceOverride)
	mgr := mustManager(cwd, workspaceOverride)
	cat, _ := catalog.Load(workspace.CatalogPath(root))
	fetchCfg := buildFetchConfig(root, cat)
	if err := serve.Serve(mgr, fetchCfg, port); err != nil {
		fatalf("serve: %v", err)
	}
}

// ---------------------------------------------------------------------------
// read  (handles both local files and remote URLs)
// ---------------------------------------------------------------------------

func cmdRead(cwd, workspaceOverride string, args []string) {
	// Parse --lines N-M flag.
	lineStart, lineEnd := 0, 0
	if idx := indexOf(args, "--lines"); idx >= 0 && idx+1 < len(args) {
		parseLineRange(args[idx+1], &lineStart, &lineEnd)
		args = append(args[:idx], args[idx+2:]...)
	}

	// Parse --toc flag.
	tocOnly := false
	if idx := indexOf(args, "--toc"); idx >= 0 {
		tocOnly = true
		args = append(args[:idx], args[idx+1:]...)
	}

	// Parse --grep <pattern> flag.
	grepPattern := ""
	if idx := indexOf(args, "--grep"); idx >= 0 && idx+1 < len(args) {
		grepPattern = args[idx+1]
		args = append(args[:idx], args[idx+2:]...)
	}

	// Parse grep option flags.
	grepOpts := fetch.GrepOptions{ContextLines: 2, MaxMatches: 50}
	if idx := indexOf(args, "--ignore-case"); idx >= 0 {
		grepOpts.IgnoreCase = true
		args = append(args[:idx], args[idx+1:]...)
	}
	if idx := indexOf(args, "--literal"); idx >= 0 {
		grepOpts.Literal = true
		args = append(args[:idx], args[idx+1:]...)
	}
	if idx := indexOf(args, "--context"); idx >= 0 && idx+1 < len(args) {
		fmt.Sscanf(args[idx+1], "%d", &grepOpts.ContextLines)
		args = append(args[:idx], args[idx+2:]...)
	}
	if idx := indexOf(args, "--limit"); idx >= 0 && idx+1 < len(args) {
		fmt.Sscanf(args[idx+1], "%d", &grepOpts.MaxMatches)
		args = append(args[:idx], args[idx+2:]...)
	}

	if len(args) == 0 {
		fatalf("toolbox read: missing path or URL\nUsage: toolbox read [--lines N-M] [--grep <pattern>] [--toc] <url-or-file>")
	}
	target := args[0]
	isURL := strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://")

	// --toc is only meaningful for markdown content.
	if tocOnly && !isURL && !isMarkdownFile(target) {
		fatalf("read --toc: only available for markdown files (.md) and remote URLs")
	}

	// Resolve the content: fetch for URLs, read directly for local files.
	var result *fetch.Result
	if isURL {
		root := resolveRoot(cwd, workspaceOverride)
		var cat *catalog.Catalog
		if c, err := catalog.Load(workspace.CatalogPath(root)); err == nil {
			cat = c
		}
		cacheDir := workspace.FetchCachePath(root)
		var err error
		result, err = fetch.Fetch(target, cacheDir, buildFetchConfig(root, cat))
		if err != nil {
			fatalf("read: %v", err)
		}
	} else {
		var err error
		result, err = fetch.Read(target)
		if err != nil {
			fatalf("read: %v", err)
		}
	}

	switch {
	case lineStart > 0:
		out, err := fetch.ReadLines(result.CachePath, lineStart, lineEnd)
		if err != nil {
			fatalf("read: %v", err)
		}
		fmt.Print(out)
	case grepPattern != "":
		out, err := fetch.GrepFile(result.CachePath, grepPattern, grepOpts)
		if err != nil {
			fatalf("read --grep: %v", err)
		}
		fmt.Print(out)
	case tocOnly:
		fmt.Print(fetch.FormatTOC(result))
	default:
		fmt.Print(fetch.FormatContent(result, fetch.DefaultContentLimit))
	}
}

func isMarkdownFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
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

func jsonMarshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
