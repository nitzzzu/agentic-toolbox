# Toolbox - Technical Documentation

## Introduction

Toolbox is a command-line tool that gives AI agents a clean, reliable way to run commands inside isolated containers — without requiring complex integration protocols like MCP. Instead of teaching an agent how to talk to various tools, you define which containers exist, which tools they contain, and Toolbox handles the rest: routing, starting, executing, and cleaning up.

The core idea is simple: an agent calls `toolbox exec "playwright screenshot https://example.com"`. Toolbox reads the project's `.toolbox/catalog.yaml`, identifies `playwright` as the primary tool in the command, finds the container that handles `playwright`, ensures it's running, executes the command inside it, streams the output back, and exits with the same exit code the command returned. The agent never needs to know which container it ran in.

Every project has its own `.toolbox/catalog.yaml` that defines the containers available in that workspace. Containers are started once and reused across multiple exec calls, making repeated commands fast. When a container isn't needed, `toolbox down` stops and removes it. Because containers run in the host's Podman or Docker daemon, they have full access to the host's workspace directory, mounted at `/workspace` inside every container.

The tool supports three container backends: Podman (preferred, rootless), Docker (fallback), and SSH (for remote execution on another machine). The active backend is auto-detected or specified in the catalog.

Environment variables are carefully layered: host variables matching allow-list patterns are forwarded, a shared `.toolbox/env` file provides committed configuration, a `.toolbox/env.local` file holds secrets that are never committed, and each container can declare its own overrides. Secrets are detected by keyword and stored separately.

Each execution is logged to `.toolbox/exec.log` in JSONL format, giving agents and operators a structured history of what ran, in which container, at what time, and with what exit code.

Beyond the CLI, Toolbox ships with a lightweight HTTP API server (`toolbox serve`, port 7070 by default) that exposes exec, status, and file system operations over localhost. This lets agents that prefer HTTP calls to interact with the workspace and execute container commands without spawning subprocess.

Toolbox is a single statically-linked binary with no runtime dependencies — only Podman or Docker is required on the host.

---

## 1. Overview

### Core Concepts
| Concept | Description |
|---------|-------------|
| Workspace | A project directory containing `.toolbox/catalog.yaml`. Detected by walking up from cwd. |
| Catalog | YAML file defining containers, routing rules, environment, and limits for a workspace. |
| Container slot | A named container defined in the catalog (e.g., `base`, `browser`). |
| Handle | A primary tool name (e.g., `playwright`) that routes exec commands to a specific slot. |
| Fallback | The default container that catches commands with no matching handle. Exactly one required. |
| Ephemeral | A `--rm` container started fresh for one command and removed immediately after. |
| Lazy start | A container started automatically on first exec if it wasn't started by `toolbox up`. |

### Command Routing
| Routing Mode | Trigger | Behavior |
|-------------|---------|----------|
| Auto (handle-based) | `toolbox exec "playwright ..."` | Extract first word → match against `handles[]` in catalog |
| Fallback | No handle matches | Route to the container with `fallback: true` |
| Forced | `--container base` flag | Skip routing, use named container directly |

**Routing logic**: Strip path prefix from command's first token (e.g., `/usr/bin/python3` → `python3`). Scan non-fallback containers for a matching `handles[]` entry. If none match, use the fallback container. If no fallback is defined, fail.

### Runtime Backends
| Backend | Key | Requirement | Notes |
|---------|-----|-------------|-------|
| Podman | `podman` | Podman CLI | Preferred. Rootless (`--userns=keep-id`); files written in containers appear owned by host user. |
| Docker | `docker` | Docker CLI | Fallback. Files written inside containers appear owned by `root`. |
| SSH | `ssh` | SSH access to remote host with Podman | All container operations run remotely over SSH. |

**Detection order**: `TOOLBOX_RUNTIME` env var → `runtime:` in catalog → `podman` available → `docker` available → error.

---

## 2. Configuration Format

### Catalog File (`.toolbox/catalog.yaml`)

```yaml
version: 1

# Runtime: podman | docker | ssh | (auto-detect)
runtime: podman

# SSH backend config (required if runtime: ssh)
ssh:
  host: user@remote.host
  identity: ~/.ssh/id_ed25519  # optional
  port: 22                      # optional, default 22

# Global environment forwarding rules
env:
  forward:
    - "OPENAI_*"
    - "AWS_*"
    - "DATABASE_*"
  deny:
    - "HOME"
    - "USER"
    - "SHELL"

# Global default timeout (CLI flag overrides, per-container overrides)
timeout: 5m

containers:
  base:
    image: ghcr.io/nitzzzu/toolbox-base:latest
    description: "General purpose tools"
    fallback: true             # catches unmatched commands
    shell: sh                  # default: sh

    env:
      MY_VAR: "value"
      FROM_HOST: "${HOST_VAR}" # ${} interpolation from host env

    limits:
      cpu: "2"        # fractional cores: "0.5", "1.5", "2"
      memory: "4GB"   # units: MB, GB (e.g., "512MB", "4GB")
      pids: 512       # max processes in container

    network: bridge   # bridge | none | host | <custom-network>
    timeout: 2m       # per-container override

  browser:
    image: ghcr.io/nitzzzu/toolbox-browser:latest
    handles:
      - playwright
      - chromium
      - crawl4ai
    limits:
      cpu: "4"
      memory: "8GB"
```

### Workspace File Structure
```
my-project/
├── .toolbox/
│   ├── catalog.yaml    # Container definitions (commit this)
│   ├── env             # Shared non-secret config (commit this)
│   ├── env.local       # Secrets — never committed (auto-gitignored)
│   ├── .gitignore      # Auto-generated by toolbox init
│   └── exec.log        # Exec history JSONL (gitignore recommended)
└── src/
```

### Environment Variable Precedence
| Layer | Source | Priority |
|-------|--------|----------|
| 1 (lowest) | Host environment, filtered by `forward`/`deny` rules | Lowest |
| 2 | `.toolbox/env` (committed shared config) | |
| 3 | `.toolbox/env.local` (gitignored secrets) | |
| 4 (highest) | Per-container `env:` in catalog.yaml | Highest |

### Sensitive Key Detection
Variables with these keywords in their name go to `env.local` (via `toolbox env set`):

`KEY` · `SECRET` · `TOKEN` · `PASSWORD` · `PASSWD` · `CREDENTIAL` · `PRIVATE`

### Timeout Resolution
| Source | Example | Priority |
|--------|---------|----------|
| CLI flag | `--timeout 30s` | Highest |
| Per-container in catalog | `containers.base.timeout: 2m` | Medium |
| Global in catalog | `timeout: 5m` | Lowest |
| Not set | — | No timeout (runs indefinitely) |

---

## 3. Exec Processing Flow

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                          TOOLBOX EXEC FLOW                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  INPUT: toolbox exec [--container X] [--timeout T] [--ephemeral] "CMD"          │
│                                                                                  │
│  1. WORKSPACE DETECTION                                                          │
│     ├─ Walk up from cwd looking for .toolbox/ directory                          │
│     └─ Fall back to cwd itself if not found                                      │
│                                                                                  │
│  2. CATALOG LOAD                                                                 │
│     ├─ Parse .toolbox/catalog.yaml                                               │
│     ├─ BLOCKER: File not found → Error "no catalog found"                        │
│     └─ BLOCKER: YAML invalid → parse error                                       │
│                                                                                  │
│  3. RUNTIME DETECTION                                                            │
│     ├─ Check TOOLBOX_RUNTIME env var → podman | docker | ssh                    │
│     ├─ Check catalog.yaml runtime: field                                         │
│     ├─ Check: podman available?                                                  │
│     ├─ Check: docker available?                                                  │
│     └─ BLOCKER: None found → error with install instructions                    │
│                                                                                  │
│  4. CONTAINER RESOLUTION                                                         │
│     ├─ --container flag set? → Use named slot directly                           │
│     │   └─ BLOCKER: Named slot not in catalog → error                           │
│     └─ Auto-route:                                                               │
│         ├─ Extract primary tool (first token, strip path prefix)                 │
│         ├─ Match against handles[] of non-fallback containers                   │
│         ├─ Handle matched? → Use that container                                 │
│         └─ No match? → Use fallback container                                   │
│             └─ BLOCKER: No fallback defined → error                              │
│                                                                                  │
│  5. ENVIRONMENT MERGE                                                            │
│     ├─ Filter host env (forward/deny glob rules)                                 │
│     ├─ Layer .toolbox/env                                                        │
│     ├─ Layer .toolbox/env.local (secrets)                                        │
│     └─ Layer per-container env (catalog) — highest priority                      │
│                                                                                  │
│  6. EXECUTION                                                                    │
│     ├─ --ephemeral?                                                              │
│     │   └─ runtime.RunEphemeral() → podman/docker run --rm ...                 │
│     └─ Persistent:                                                               │
│         ├─ mgr.EnsureRunning() → pull + remove stopped + docker run             │
│         │   └─ Wait up to 30s for container to be ready                         │
│         └─ runtime.Exec() → podman/docker exec CONTAINER SHELL -c "CMD"         │
│             └─ [SSH] ssh host "podman exec CONTAINER SHELL -c CMD"              │
│                                                                                  │
│  7. LOGGING                                                                      │
│     └─ Append JSON entry to .toolbox/exec.log                                   │
│         (container, image, command, exit code, duration ms, ephemeral flag)     │
│                                                                                  │
│  OUTPUT: exit with same code as command inside container                         │
│                                                                                  │
│  EXAMPLE SCENARIO:                                                               │
│     1. cwd = /projects/my-app/src                                                │
│     2. Workspace detected: /projects/my-app                                      │
│     3. Catalog loaded: 2 containers (base + browser)                             │
│     4. Command: "playwright screenshot https://example.com"                      │
│     5. Primary tool: "playwright" → matches browser.handles[]                   │
│     6. browser container not running → lazy-start (pull + run)                  │
│     7. exec "playwright screenshot https://example.com" inside browser           │
│     8. Exit code 0 → logged → os.Exit(0)                                        │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**Implementation:** `main.go cmdExec()` → `container.Manager.ExecCommand()`

### How It Works

1. **Workspace Detection** - Walks up from current directory until `.toolbox/` is found. Allows exec to work from any subdirectory of the project.
2. **Catalog Load** - Parses `.toolbox/catalog.yaml` for container definitions, handles, limits, and env rules.
3. **Runtime Detection** - Selects Podman, Docker, or SSH backend. Respects explicit configuration over auto-detection.
4. **Container Resolution** - Extracts the command's first word (stripping path prefix) and scans catalog handles. Falls back to the designated fallback container.
5. **Environment Merge** - Builds the container's environment from four layers, with per-container overrides winning.
6. **Execution** - Either creates an ephemeral container (`--rm`) or lazy-starts the persistent container and execs into it.
7. **Logging** - Appends a structured JSONL record to `.toolbox/exec.log` regardless of exit code.

---

## 4. Container Lifecycle Flow

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                        CONTAINER LIFECYCLE                                       │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  toolbox up                                                                      │
│     For each container in catalog:                                               │
│     ├─ Pull image (docker pull IMAGE)                                            │
│     ├─ Check IsRunning(containerName)                                            │
│     │   └─ Running? → "already running ✓" → skip                                │
│     ├─ Remove stopped container if exists (docker rm --force)                   │
│     └─ docker run -d --name NAME -v WORKSPACE:/workspace ...                   │
│                                                                                  │
│  toolbox exec (lazy start path — EnsureRunning)                                 │
│     ├─ Check IsRunning(containerName)                                            │
│     │   └─ Running? → proceed to exec                                            │
│     ├─ Pull image (warn and continue if fails — use local cache)                │
│     ├─ Remove stopped container if exists (docker rm --force)                   │
│     ├─ docker run -d --name NAME ...                                             │
│     └─ Poll IsRunning every 500ms up to 30 seconds                              │
│         └─ BLOCKER: 30s elapsed without running → timeout error                 │
│                                                                                  │
│  toolbox restart [SLOT]                                                          │
│     ├─ docker stop NAME (ignore error)                                           │
│     ├─ docker rm --force NAME (ignore error)                                    │
│     └─ docker run -d --name NAME ... (full recreation)                          │
│                                                                                  │
│  toolbox down                                                                    │
│     ├─ docker ps -a --filter name=toolbox-{workspace}-*                         │
│     ├─ For each matching container:                                              │
│     │   ├─ docker stop NAME                                                      │
│     │   └─ docker rm --force NAME                                                │
│     └─ Only removes containers for this workspace (prefix-filtered)             │
│                                                                                  │
│  CONTAINER NAMING                                                                │
│     Format: toolbox-{sanitized-dirname}-{8-hex-hash}-{slot}                    │
│     Example: toolbox-my-app-a1b2c3d4-base                                       │
│     Hash: FNV-1a of normalized workspace path (collision prevention)            │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

**Implementation:** `internal/container/lifecycle.go Manager.Up()`, `Manager.EnsureRunning()`, `Manager.Down()`

### Volume Mounting

Every container is started with these volume mounts:
- `-v {workspaceRoot}:/workspace` — host workspace mounted read-write at `/workspace`
- `-v /workspace/.toolbox` — anonymous volume shadows `.toolbox/` inside container (prevents container from writing exec logs or modifying env files)

Working directory inside container: `/workspace`

---

## 5. All Blockers Reference

| Stage | Condition | Error |
|-------|-----------|-------|
| Runtime detection | No podman or docker found | "no container runtime found" + install instructions |
| Catalog load | `.toolbox/catalog.yaml` missing | "no catalog found" |
| Catalog load | Invalid YAML | YAML parse error |
| Catalog validation | Container missing `image:` | "container X: image is required" |
| Catalog validation | No fallback container | "no fallback container defined" |
| Container resolution | `--container X` but X not in catalog | "container X not found" |
| Container resolution | No fallback defined and no handle matches | "no fallback container defined" |
| Container start | Image pull fails (up path) | "pull IMAGE: error" |
| Container start | 30s wait-ready timeout exceeded | "container X did not become ready within 30s" |
| Container exec | Command fails in container | exit code preserved, no error |
| Container exec | Timeout exceeded | exit code 1, "exec timed out after Xs" |
| HTTP API | Path traversal attempt (`../`) | HTTP 400 |
| SSH runtime | SSH config missing when runtime=ssh | "runtime=ssh requires [ssh] config" |

---

## 6. HTTP API Reference

The `toolbox serve` command starts an HTTP server on `127.0.0.1:7070` (never exposed to external network by default).

**Implementation:** `internal/serve/server.go`

### Endpoints

| Method | Route | Description |
|--------|-------|-------------|
| `GET` | `/health` | Health check with workspace and runtime info |
| `GET` | `/status` | List running toolbox containers |
| `POST` | `/exec` | Execute a command in a container |
| `GET` | `/workspace` | List workspace directory contents |
| `GET` | `/workspace/{path}` | Read a workspace file |
| `PUT` | `/workspace/{path}` | Write a workspace file |
| `DELETE` | `/workspace/{path}` | Delete a workspace file |
| `GET` | `/find` | Glob file search |
| `GET` | `/grep` | Content search |

### GET /health
```json
{"ok": true, "workspace": "/path/to/project", "runtime": "podman"}
```

### GET /status
```json
[
  {"Name": "toolbox-my-app-a1b2c3-base", "Image": "ghcr.io/nitzzzu/base:latest",
   "Status": "Up 3 hours", "Created": "2024-01-01 10:00:00"}
]
```

### POST /exec
Request:
```json
{"cmd": "python3 train.py", "container": "", "timeout": "30s", "ephemeral": false}
```
Response:
```json
{"stdout": "Training complete\n", "stderr": "", "exit": 0, "ms": 14320}
```

Fields: `cmd` (required), `container` (optional, overrides routing), `timeout` (optional, e.g. `"30s"`, `"2m"`), `ephemeral` (optional bool).

#### Streaming mode (recommended for AI agents)

Add `?stream=true` or set `Accept: application/x-ndjson` to receive output in real time as NDJSON (one JSON object per line):

```
{"type":"stdout","text":"Epoch 1/10\n"}
{"type":"stdout","text":"loss: 0.342\n"}
{"type":"stderr","text":"warning: low memory\n"}
{"type":"result","exit":0,"ms":14320}
```

On a system error (e.g. container fails to start) the final event is `{"type":"error","error":"..."}` instead of `"result"`. The HTTP status is always `200` once headers are sent — check the final event type to detect errors.

### GET /workspace
Query params: `?path=src/components` (optional subdirectory, default: workspace root)
```json
{
  "path": "src/components",
  "entries": [
    {"name": "App.tsx", "size": 4096, "modified": "2024-01-01T10:00:00Z", "is_dir": false},
    {"name": "utils", "size": 0, "modified": "2024-01-01T09:00:00Z", "is_dir": true}
  ]
}
```

### GET /workspace/{path}
Returns raw file bytes. Query params: `?offset=N&limit=N` for line-ranged reads (1-indexed).

### PUT /workspace/{path}
Body: raw file content. Creates parent directories automatically. Returns `204 No Content`.

### DELETE /workspace/{path}
Returns `204 No Content`.

### GET /find
Query params: `?pattern=**/*.ts&path=src&limit=1000`

Returns one relative file path per line. Automatically skips `.git/` and `node_modules/`. Supports `**` for recursive directory matching.

### GET /grep
Query params: `?pattern=TODO&glob=*.py&ignore_case=true&context=2&limit=100`

Returns ripgrep-style output:
```
src/app.py:42: # TODO: fix this
src/app.py-41- def process():
src/app.py-43-     pass
```

Lines longer than 500 characters are truncated with `... [truncated]`.

---

## 7. CLI Commands Reference

### Execution

**`toolbox exec [OPTIONS] <command>`**

Route and execute a command in a container.

| Flag | Description |
|------|-------------|
| `--container <name>` | Force a specific container slot |
| `--timeout <duration>` | Exec timeout override (e.g. `30s`, `2m`) |
| `--ephemeral` | Run in a fresh `--rm` container |

Exit code: preserved from the container command.

**`toolbox shell [<container>]`**

Open an interactive shell. Defaults to fallback container if no argument given.

### Lifecycle

| Command | Description |
|---------|-------------|
| `toolbox up` | Pull all images and start all containers |
| `toolbox down` | Stop and remove all workspace containers |
| `toolbox pull` | Pull all images without starting containers |
| `toolbox restart [<slot>]` | Restart one container (or all if no slot given) |
| `toolbox status` | Show running containers with image, status, and creation time |
| `toolbox init` | Initialize `.toolbox/` directory structure |

### Catalog

| Command | Description |
|---------|-------------|
| `toolbox catalog list` | List containers and their handles |
| `toolbox catalog validate` | Validate catalog.yaml structure |

### Environment

| Command | Description |
|---------|-------------|
| `toolbox env list` | Show merged environment (secrets masked) |
| `toolbox env set KEY=VALUE [...]` | Set workspace variables (secrets auto-routed to env.local) |
| `toolbox env unset KEY` | Remove variable from both env files |

### Observability

| Command | Description |
|---------|-------------|
| `toolbox log [--tail N] [--json]` | Show exec history. Default tail: 50. `--json` for JSONL output. |
| `toolbox serve [--port N]` | Start HTTP API server. Default port: 7070. |

### Global Flags

| Flag | Description |
|------|-------------|
| `--workspace <path>` | Override workspace root (also: `TOOLBOX_WORKSPACE` env var) |
| `--version` / `-v` | Print version and exit |
| `--help` / `-h` | Print usage and exit |

---

## 8. Core Classes Reference

### Internal Packages

| Package | Path | Purpose |
|---------|------|---------|
| `catalog` | `internal/catalog/` | Parse catalog.yaml, route commands to containers |
| `container` | `internal/container/` | Runtime abstraction, container lifecycle, exec |
| `workspace` | `internal/workspace/` | Workspace detection, path resolution, naming |
| `env` | `internal/env/` | Env variable filtering, layering, file I/O |
| `log` | `internal/log/` | Exec history JSONL logging |
| `serve` | `internal/serve/` | HTTP API server |

### Key Types

| Type | Package | Purpose |
|------|---------|---------|
| `Catalog` | `catalog` | Top-level parsed catalog.yaml |
| `Container` | `catalog` | Single container slot definition |
| `EnvRules` | `catalog` | Forward/deny glob patterns for host env |
| `ResourceLimits` | `catalog` | CPU, memory, PIDs limits |
| `SSHConfig` | `catalog` | SSH backend connection config |
| `Manager` | `container` | High-level container lifecycle orchestration |
| `Runtime` | `container` | Interface: Podman, Docker, or SSH |
| `RunOpts` | `container` | Options for starting a persistent container |
| `ExecOpts` | `container` | Options for exec into running container |
| `EphemeralOpts` | `container` | Options for `--rm` one-shot container |
| `ContainerStatus` | `container` | Name, image, status, created time |
| `Entry` | `log` | Single exec log record |

### Key Methods

| Method | Package | Description |
|--------|---------|-------------|
| `catalog.Load(path)` | `catalog` | Parse catalog.yaml from disk |
| `(*Catalog).Resolve(cmd)` | `catalog` | Route command → container slot |
| `(*Catalog).Validate()` | `catalog` | Return list of validation errors |
| `workspace.Root(cwd)` | `workspace` | Find workspace root by walking up dirs |
| `workspace.ContainerName(root, slot)` | `workspace` | Generate stable container name |
| `env.Merged(root, rules, containerEnv)` | `env` | Build final env slice for container |
| `container.NewManager(root, cat)` | `container` | Create manager, auto-detect runtime |
| `(*Manager).Up()` | `container` | Pull images + start all containers |
| `(*Manager).Down()` | `container` | Stop and remove all workspace containers |
| `(*Manager).ExecCommand(opts)` | `container` | Main exec entry point |
| `(*Manager).EnsureRunning(slot, ct)` | `container` | Lazy-start a container if not running |
| `(*Manager).Shell(slot)` | `container` | Open interactive shell |
| `log.Append(root, entry)` | `log` | Append exec record to exec.log |
| `log.Read(root, tail)` | `log` | Read last N entries from exec.log |

---

## 9. Data Models Reference

### Catalog
| Field | Type | Description |
|-------|------|-------------|
| `Version` | `int` | Schema version (always 1) |
| `Runtime` | `string` | `podman` \| `docker` \| `ssh` \| empty (auto) |
| `SSH` | `*SSHConfig` | SSH backend config (required if runtime=ssh) |
| `Env` | `EnvRules` | Global host env forward/deny rules |
| `Containers` | `map[string]Container` | Named container slots |
| `Timeout` | `string` | Global default timeout (e.g. `"5m"`) |

### Container
| Field | Type | Description |
|-------|------|-------------|
| `Image` | `string` | Container image URI (required) |
| `Description` | `string` | Human-readable purpose |
| `Handles` | `[]string` | Primary tool names routed to this container |
| `Env` | `map[string]string` | Per-container env vars (highest priority) |
| `Fallback` | `bool` | Catch commands with no matching handle |
| `Shell` | `string` | Shell binary (default: `sh`) |
| `Limits` | `ResourceLimits` | CPU, memory, PIDs constraints |
| `Network` | `string` | `bridge` \| `none` \| `host` \| custom name |
| `Timeout` | `string` | Per-container timeout override |

### ResourceLimits
| Field | Type | Example |
|-------|------|---------|
| `CPU` | `string` | `"2"`, `"0.5"`, `"1.5"` |
| `Memory` | `string` | `"4GB"`, `"512MB"` |
| `PIDs` | `int` | `512`, `1024` |

### SSHConfig
| Field | Type | Description |
|-------|------|-------------|
| `Host` | `string` | SSH target (e.g. `user@remote.host`) |
| `Identity` | `string` | Private key path (optional) |
| `Port` | `int` | SSH port (default: 22) |

### Log Entry
| Field | Type | Description |
|-------|------|-------------|
| `TS` | `time.Time` | Execution timestamp |
| `Container` | `string` | Container slot name |
| `Image` | `string` | Container image URI |
| `Command` | `string` | Executed command string |
| `Ephemeral` | `bool` | Whether run as ephemeral (`--rm`) |
| `ExitCode` | `int` | Exit code returned by command |
| `Ms` | `int64` | Duration in milliseconds |

### ExecCommand Options
| Field | Type | Description |
|-------|------|-------------|
| `Command` | `string` | Command to execute |
| `ForceContainer` | `string` | Override routing (slot name) |
| `Timeout` | `time.Duration` | Override catalog timeout (0 = use catalog) |
| `Ephemeral` | `bool` | Use fresh `--rm` container |
| `Stdout` | `io.Writer` | Custom stdout (nil = os.Stdout) |
| `Stderr` | `io.Writer` | Custom stderr (nil = os.Stderr) |

---

## 10. Environment Variable Reference

### Runtime Configuration
| Variable | Values | Description |
|----------|--------|-------------|
| `TOOLBOX_RUNTIME` | `podman` \| `docker` \| `ssh` | Override runtime auto-detection |
| `TOOLBOX_WORKSPACE` | Path | Override workspace root detection |

### `toolbox init` Default Forward Rules
```
OPENAI_*, ANTHROPIC_*, AWS_*, AZURE_*, GOOGLE_*, DATABASE_*, REDIS_*, CI, DEBUG
```

### `toolbox init` Default Deny Rules
```
HOME, USER, SHELL, TMPDIR
```

---

## 11. Container Naming Convention

Format: `toolbox-{sanitized-dirname}-{8-hex-hash}-{slot}`

| Part | Source | Example |
|------|--------|---------|
| `toolbox-` | Constant prefix | `toolbox-` |
| `{sanitized-dirname}` | Workspace folder name, lowercase alphanumeric + hyphens | `my-app` |
| `{8-hex-hash}` | FNV-1a hash of normalized full workspace path | `a1b2c3d4` |
| `{slot}` | Container slot name from catalog | `base`, `browser` |

**Full example:** `toolbox-my-app-a1b2c3d4-base`

**Collision prevention:** Two projects in different locations but with the same folder name (e.g., `/projects/v1/my-app` and `/projects/v2/my-app`) produce different hashes and cannot interfere with each other's containers.

---

## 12. Troubleshooting

### Common Issues

| Problem | Cause | Solution |
|---------|-------|----------|
| `no container runtime found` | Podman/Docker not installed or not in PATH | Install Podman or Docker; or set `TOOLBOX_RUNTIME` |
| `Conflict. The container name ... is already in use` | Stopped container with same name exists | Fixed in current version (remove is called before create). Run `toolbox down` then `toolbox up` to reset. |
| `no fallback container defined` | Catalog has no container with `fallback: true` | Add `fallback: true` to one container in catalog.yaml |
| `container X did not become ready within 30s` | Container crashed on startup or image issues | Check image tag; run `toolbox up` manually and inspect docker logs |
| Command routes to wrong container | Handle not defined for primary tool | Add the tool name to `handles:` in the correct container, or use `--container` flag |
| Secrets appear in committed files | Secret keyword not recognized | Ensure key name contains: KEY, SECRET, TOKEN, PASSWORD, PASSWD, CREDENTIAL, or PRIVATE |
| Files written in container owned by root | Using Docker instead of Podman | Switch to Podman for rootless UID mapping (`--userns=keep-id`) |
| `toolbox exec` hangs | Command inside container is blocking | Set `--timeout` flag or configure `timeout:` in catalog |
| HTTP API not reachable | Server not started or wrong port | Run `toolbox serve`; check `--port` flag; server binds to `127.0.0.1` only |
| SSH runtime: `StrictHostKeyChecking` prompt | First connection to SSH host | Toolbox uses `StrictHostKeyChecking=accept-new` — first connection auto-accepts |

### Debugging Exec History
```bash
# Show last 20 executions
toolbox log --tail 20

# Machine-readable JSONL for filtering
toolbox log --json | grep '"exit":1' | jq '{cmd: .command, ms: .ms}'
```

### Verifying Routing
```bash
# Check which container a command would route to
toolbox catalog list

# Force a specific container
toolbox exec --container browser "playwright screenshot https://example.com"
```

---

## 13. Summary

### Exec Routing Decision Tree
| Condition | Result |
|-----------|--------|
| `--container X` flag set | Route to slot X directly |
| First word of command matches a handle | Route to that container |
| No handle matches, fallback defined | Route to fallback container |
| No handle matches, no fallback | Error |

### Container State Transitions
| From State | Action | To State |
|-----------|--------|----------|
| Not exists | `toolbox up` | Running |
| Not exists | `toolbox exec` | Running (lazy-start) |
| Running | `toolbox exec` | Running (no change) |
| Running | `toolbox down` | Removed |
| Running | `toolbox restart` | Running (fresh) |
| Stopped | `toolbox up` | Running (remove + recreate) |
| Stopped | `toolbox exec` | Running (remove + recreate via lazy-start) |

### Key Rules
1. Exactly one container in the catalog must have `fallback: true`.
2. Container names are stable and workspace-scoped — two projects never share a container even with identical folder names.
3. `.toolbox/env.local` is always gitignored and never mounted inside containers.
4. Timeout resolution: CLI flag wins over per-container over global catalog.
5. Ephemeral containers bypass lazy-start and are removed immediately after exit.
6. The HTTP server binds to `127.0.0.1` only and is never externally reachable by default.

### Common Scenarios
| Scenario | Behavior |
|----------|----------|
| Running `toolbox exec` with no containers started | Lazy-starts the routed container, waits up to 30s, executes |
| Running `toolbox up` when containers already running | Skips running containers, prints "already running ✓" |
| Running `toolbox up` when containers stopped | Removes stopped containers, recreates from image |
| Running `toolbox exec --ephemeral "..."` | Fresh container per execution, removed after, no persistent state |
| Two projects with same folder name | Different container names due to path hash — no interference |
| Secret var set via `toolbox env set` | Auto-detected by keyword, written to `env.local` only |

---

