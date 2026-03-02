<div align="center">

```
 ████████╗ ██████╗  ██████╗ ██╗     ██████╗  ██████╗ ██╗  ██╗
    ██╔══╝██╔═══██╗██╔═══██╗██║     ██╔══██╗██╔═══██╗╚██╗██╔╝
    ██║   ██║   ██║██║   ██║██║     ██████╔╝██║   ██║ ╚███╔╝
    ██║   ██║   ██║██║   ██║██║     ██╔══██╗██║   ██║ ██╔██╗
    ██║   ╚██████╔╝╚██████╔╝███████╗██████╔╝╚██████╔╝██╔╝ ██╗
    ╚═╝    ╚═════╝  ╚═════╝ ╚══════╝╚═════╝  ╚═════╝ ╚═╝  ╚═╝
```

**Container-native tool execution for AI agents.**
Route agent commands to isolated containers. No MCP. No protocol. Just exec.

[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![Podman](https://img.shields.io/badge/Runtime-Podman%20%7C%20Docker-892CA0?style=flat-square&logo=podman&logoColor=white)](https://podman.io)
[![Zero deps](https://img.shields.io/badge/Dependencies-Zero-orange?style=flat-square)](#)

</div>

---

```
agent calls  →  toolbox exec "playwright screenshot https://example.com"
                     │
                     ▼
              reads .toolbox/catalog.yaml
              extracts primary tool: "playwright"
              matches handles[] → browser container
                     │
                     ▼
              podman exec toolbox-myproject-browser bash -c "playwright ..."
                     │
                     ▼
              output streams back to agent
```

The agent doesn't know or care about containers. It calls `bash`. Toolbox handles the rest.

---

## Why

AI agents execute directly on the host — no isolation, no reproducibility, secrets in the environment. MCP adds protocol overhead and bloats context windows.

Toolbox is the missing layer: **containers as tools, exec as the protocol.**

| | MCP | toolbox |
|---|---|---|
| Protocol overhead | JSON-RPC schema per tool | None — just exec |
| Agent context bloat | Tool schemas in every prompt | Zero |
| Container support | Wrapper around containers | Containers *are* the tools |
| Framework lock-in | Framework-specific integration | 4 lines in any language |
| Secret management | Manual per-tool config | `.toolbox/env.local` forwarded automatically |
| Resource limits | Manual per-tool config | Declarative in catalog.yaml |
| Exec timeout | Manual per-tool config | `--timeout 30s` or catalog default |
| Network isolation | Manual networking | `network: none` in catalog.yaml |
| Ephemeral execution | Full container lifecycle required | `--ephemeral` flag |
| Exec history | External logging | Built-in `toolbox log` |
| HTTP API | Custom integration | `toolbox serve` |
| Workspace file API | Not included | Read, write, edit, find, grep over HTTP |

---

## Install

```bash
# Linux
curl -fsSL https://github.com/nitzzzu/toolbox/releases/latest/download/toolbox-linux-amd64 \
  -o /usr/local/bin/toolbox && chmod +x /usr/local/bin/toolbox

# macOS (Apple Silicon)
curl -fsSL https://github.com/nitzzzu/toolbox/releases/latest/download/toolbox-darwin-arm64 \
  -o /usr/local/bin/toolbox && chmod +x /usr/local/bin/toolbox

# macOS (Intel)
curl -fsSL https://github.com/nitzzzu/toolbox/releases/latest/download/toolbox-darwin-amd64 \
  -o /usr/local/bin/toolbox && chmod +x /usr/local/bin/toolbox

# Build from source (Go 1.22+)
go install github.com/nitzzzu/toolbox/cmd/toolbox@latest
```

Requires [Podman](https://podman.io/getting-started/installation) (preferred) or Docker.

---

## Quickstart

```bash
# 1. Initialize your project
cd my-project
toolbox init

# 2. Add secrets
echo "OPENAI_API_KEY=sk-..." >> .toolbox/env.local

# 3. Start containers
toolbox up

# 4. Run anything
toolbox exec "python3 train.py --epochs 100"
toolbox exec "playwright screenshot https://example.com out.png"
toolbox exec "duckdb data.parquet 'SELECT count(*) FROM data'"
toolbox exec "rg 'TODO' src/ --json | jq '.matches'"

# Run with a 30s timeout
toolbox exec --timeout 30s "python3 slow_script.py"

# Run in a fresh ephemeral container (no persistent state)
toolbox exec --ephemeral "hostname"

# View exec history
toolbox log --tail 20

# Expose HTTP API for programmatic use
toolbox serve --port 7070
```

---

## The Catalog

Each workspace has a `.toolbox/catalog.yaml` that declares available containers, routing rules, resource limits, and isolation settings:

```yaml
# .toolbox/catalog.yaml
version: 1

# Global default timeout for all execs (overridable per-container and per-call).
timeout: 5m

env:
  forward: ["OPENAI_*", "ANTHROPIC_*", "AWS_*", "DATABASE_*"]
  deny:    ["HOME", "USER", "SHELL"]

containers:
  base:
    image: ghcr.io/nitzzzu/toolbox-base:latest
    description: "python3.14, node22, uv, rg, fd, jq, duckdb, dasel, hurl, delta, git, curl, ..."
    fallback: true                          # catches everything unmatched
    limits:
      cpu: "2"                              # max 2 CPU cores
      memory: "4GB"                        # max 4 GB RAM
      pids: 512                            # max process count
    timeout: 2m                            # override global default

  browser:
    image: ghcr.io/nitzzzu/toolbox-browser:latest
    handles: [playwright, crawl4ai, chromium]
    env:
      PLAYWRIGHT_BROWSERS_PATH: /ms-playwright
    limits:
      cpu: "4"
      memory: "8GB"

```

**Routing** is first-token based: `playwright screenshot ...` → extract `playwright` → match `handles[]` → route to `browser`. Everything else hits the `fallback`.

---

## Resource Limits

Declare resource constraints per container in `catalog.yaml`. Limits are enforced by the container runtime (`--cpus`, `--memory`, `--pids-limit`):

```yaml
containers:
  base:
    image: ghcr.io/nitzzzu/toolbox-base:latest
    fallback: true
    limits:
      cpu: "2"       # fractional cores allowed: "0.5", "1.5"
      memory: "4GB"  # units: MB, GB — e.g. "512MB", "4GB"
      pids: 512      # max processes in the container
```

Limits are applied when the container starts (`toolbox up` or lazy-start on first exec). Verify with:

```bash
podman inspect toolbox-myproject-base | grep -i cpus
```

---

## Network Isolation

Set `network: none` on any container to cut off outbound network access. The container can still exec normally but cannot reach the internet:

```yaml
containers:
  sandbox:
    image: ghcr.io/nitzzzu/toolbox-base:latest
    fallback: true
    network: none    # no internet access
```

Other values: `host` (share host network), `bridge` (default), or any named podman/docker network.

---

## Exec Timeout

Three levels of timeout, resolved in priority order (highest wins):

```
CLI flag  >  per-container catalog.yaml  >  global catalog.yaml
```

```bash
# CLI — overrides everything
toolbox exec --timeout 30s "python3 slow_script.py"

# Will error after 30s: "exec timed out after 30s"
```

```yaml
# catalog.yaml — per-container and global defaults
version: 1
timeout: 5m          # global default

containers:
  base:
    image: ...
    fallback: true
    timeout: 2m      # overrides global for this container
```

Timeout applies equally to persistent container exec and ephemeral exec.

---

## Ephemeral Execution

Run a command in a fresh `--rm` container with no shared state. The container is created, runs the command, and is removed immediately:

```bash
# One-shot container — does not appear in `toolbox status`
toolbox exec --ephemeral "hostname"
toolbox exec --ephemeral "python3 -c 'import random; print(random.random())'"

# Combine with timeout for sandboxed untrusted code
toolbox exec --ephemeral --timeout 10s "python3 untrusted.py"
```

Ephemeral execs still use all catalog settings (image, limits, network, env) but skip container start/stop overhead tracking.

---

## Exec History

Every `toolbox exec` call is logged to `.toolbox/exec.log` as JSONL. The log is append-only and workspace-scoped.

```bash
# Human-readable table (last 50 entries by default)
toolbox log

# Show last 5 entries
toolbox log --tail 5

# Raw JSONL — pipe to jq for analysis
toolbox log --json | jq 'select(.exit != 0)'
toolbox log --json | jq -r '[.ts, .container, .cmd, .exit, .ms] | @tsv'
```

Each log entry:
```json
{
  "ts": "2024-01-01T10:00:00Z",
  "container": "base",
  "image": "ghcr.io/nitzzzu/toolbox-base:latest",
  "cmd": "python3 train.py --epochs 100",
  "ephemeral": false,
  "exit": 0,
  "ms": 12345
}
```

---

## HTTP API

`toolbox serve` exposes a local HTTP API for programmatic access — useful for agent frameworks that prefer HTTP over subprocess exec:

```bash
toolbox serve --port 7070   # binds to 127.0.0.1:7070 only
```

### `/health` · `/status` · `/exec`

**`GET /health`**
```json
{"ok": true, "workspace": "/path/to/project", "runtime": "podman"}
```

**`GET /status`**
```json
[{"Name": "toolbox-myproject-base", "Image": "...", "Status": "Up", "Created": "..."}]
```

**`POST /exec`**

Request:
```json
{
  "cmd": "python3 train.py",
  "container": "",        // optional: force a specific container
  "timeout": "30s",       // optional: override timeout
  "ephemeral": false      // optional: run in fresh container
}
```

Response:
```json
{
  "stdout": "Epoch 1/10...\n",
  "stderr": "",
  "exit": 0,
  "ms": 1234
}
```

Each `/exec` request is also written to the exec log.

---

## Workspace API

The workspace is the project directory, mounted at `/workspace` inside every container. The workspace API lets agents read, write, search, and edit files directly — no container exec required for file operations.

```
host filesystem ({workspaceRoot}/)
        │
        │  mounted at
        ▼
/workspace/  inside every container
        │
        │  accessible via
        ▼
HTTP API  (toolbox serve)
```

Files written via the workspace API are immediately available to `exec_command`:

```bash
# Agent writes a script → runs it in the container → reads the output
PUT  /workspace/scripts/analyze.py   ← write the script
POST /exec  {"cmd": "python3 /workspace/scripts/analyze.py"}
GET  /workspace/output/results.json  ← read the result
```

### File CRUD  —  `/workspace/{path}`

All paths are relative to the workspace root. Parent directories are created automatically on write. Path traversal (`../`) is rejected with 400.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/workspace/{path}` | Read file content (raw bytes) |
| `GET` | `/workspace/{path}?offset=N&limit=N` | Read a line range (1-indexed offset) |
| `PUT` | `/workspace/{path}` | Write file (body = raw content) |
| `DELETE` | `/workspace/{path}` | Delete a file |

**Read a file:**
```bash
curl http://localhost:7070/workspace/src/main.py
```

**Read lines 50–100 of a large file:**
```bash
curl "http://localhost:7070/workspace/logs/app.log?offset=50&limit=50"
```

**Write a file:**
```bash
curl -X PUT http://localhost:7070/workspace/data/input.csv \
     --data-binary @local-file.csv
```

**Delete a file:**
```bash
curl -X DELETE http://localhost:7070/workspace/data/temp.csv
```

### Directory Listing  —  `GET /workspace`

```bash
# List workspace root
curl http://localhost:7070/workspace

# List a subdirectory
curl "http://localhost:7070/workspace?path=src/components"
```

Response:
```json
{
  "path": "src/components",
  "entries": [
    {"name": "Button.tsx", "size": 1234, "modified": "2024-01-01T10:00:00Z", "is_dir": false},
    {"name": "utils",      "size": 0,    "modified": "2024-01-01T09:00:00Z", "is_dir": true}
  ]
}
```

### File Search  —  `GET /find`

Glob file search across the workspace. Runs on the host filesystem — no container required. Skips `.git/` and `node_modules/` automatically. Supports `**` for recursive matching.

```
GET /find?pattern=<glob>[&path=<subdir>][&limit=<n>]
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `pattern` | required | Glob pattern: `*.py`, `**/*.ts`, `src/**/*.spec.ts` |
| `path` | workspace root | Subdirectory to restrict search to |
| `limit` | 1000 | Maximum results |

```bash
# All TypeScript files
curl "http://localhost:7070/find?pattern=**/*.ts"

# Python files under src/
curl "http://localhost:7070/find?pattern=*.py&path=src"

# Test files, capped at 50
curl "http://localhost:7070/find?pattern=**/*.spec.ts&limit=50"
```

Response — relative paths, one per line:
```
src/agent.py
src/tools/bash.py
tests/test_agent.py

[50 results limit reached. Refine pattern or use limit=100 for more]
```

### Content Search  —  `GET /grep`

Regex (or literal) content search across workspace files. Runs on the host filesystem — no container required. Output format matches ripgrep: match lines use `:`, context lines use `-`.

```
GET /grep?pattern=<regex>[&path=<dir>][&glob=<filter>][&ignore_case=true][&literal=true][&context=<n>][&limit=<n>]
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `pattern` | required | Regex or literal string |
| `path` | workspace root | Directory or file to search |
| `glob` | all files | Filename filter: `*.py`, `*.ts` |
| `ignore_case` | false | Case-insensitive match |
| `literal` | false | Treat pattern as literal string |
| `context` | 0 | Lines of context before/after each match |
| `limit` | 100 | Maximum matches |

```bash
# Find all TODOs in Python files
curl "http://localhost:7070/grep?pattern=TODO&glob=*.py"

# Case-insensitive search with 2 lines of context
curl "http://localhost:7070/grep?pattern=error&ignore_case=true&context=2"

# Literal string (no regex interpretation)
curl "http://localhost:7070/grep?pattern=price%3A+%2410.00&literal=true"
```

Response — match lines (`:`) and context lines (`-`):
```
src/agent.py:42: # TODO: handle timeout
src/agent.py-43- def run(self):
src/utils.py:17: # TODO: add retry logic
src/utils.py-16- import time
src/utils.py-18- MAX_RETRIES = 3

[100 matches limit reached. Use limit=200 for more, or refine pattern]
```

Lines longer than 500 characters are truncated with `... [truncated]`.

---

## Agent Integration

### Subprocess (4 lines, any framework)

**Python** (agno, LangChain, custom loop)
```python
import subprocess

def bash(cmd: str) -> str:
    r = subprocess.run(["toolbox", "exec", cmd], capture_output=True, text=True)
    return r.stdout + r.stderr
```

**TypeScript** (pi-mono, Claude Code, custom loop)
```typescript
import { execa } from "execa";

async function bash(command: string): Promise<string> {
  const { stdout, stderr } = await execa("toolbox", ["exec", command]);
  return stdout + stderr;
}
```

**pi-mono** — use the included `toolbox.ts` extension which overrides all four core tools (bash, read, write, edit):
```bash
pi -e ./toolbox.ts
```

### HTTP API (agent frameworks that prefer HTTP)

```python
import httpx

def bash(cmd: str) -> str:
    r = httpx.post("http://localhost:7070/exec", json={"cmd": cmd})
    data = r.json()
    return data["stdout"] + data["stderr"]
```

```typescript
async function bash(command: string): Promise<string> {
  const r = await fetch("http://localhost:7070/exec", {
    method: "POST",
    body: JSON.stringify({ cmd: command }),
  });
  const { stdout, stderr } = await r.json();
  return stdout + stderr;
}
```

### Full Toolkit (Agno / Python agents)

For [Agno](https://docs.agno.com) agents, use `ToolboxTools` to get all 8 pi-mono-compatible tools wired as Agno `Toolkit` methods:

| Agno tool | Equivalent | Description |
|-----------|-----------|-------------|
| `exec_command` | `bash` | Shell command in an isolated, auto-routed container |
| `workspace_read(path, offset, limit)` | `read` | File content with 1-indexed line paging |
| `workspace_write(path, content)` | `write` | Create/overwrite, creates parent dirs |
| `workspace_edit(path, old_text, new_text)` | `edit` | Surgical replace, fuzzy-matched, must be unique |
| `workspace_find(pattern, path, limit)` | `find` | Glob search, `**` supported |
| `workspace_grep(pattern, ...)` | `grep` | Regex/literal search, context lines, glob filter |
| `workspace_list(path)` | `ls` | Directory listing with size and type |
| `workspace_delete(path)` | — | Delete a file |

```python
# .env
TOOLBOX_URL=http://localhost:7070

# agent.py
from toolbox_tools import ToolboxTools
from agno.tools.duckduckgo import DuckDuckGoTools

agent = Agent(
    tools=[DuckDuckGoTools(), ToolboxTools()],
    ...
)
```

#### Typical multi-step workflow

```
User: "Analyse this CSV, show top 10 regions by revenue"

Agent:  workspace_write("data/sales.csv", <csv content>)
        exec_command("duckdb :memory: \"SELECT region, SUM(revenue) AS total
                      FROM read_csv_auto('/workspace/data/sales.csv')
                      GROUP BY 1 ORDER BY 2 DESC LIMIT 10\"")
        workspace_delete("data/sales.csv")
        → returns formatted table

User: "Now find all TODOs in our codebase"

Agent:  workspace_grep("TODO", glob="*.py", context=1)
        → returns every match with one line of surrounding context
```

---

## Environment Variable Forwarding

Three layers, merged in priority order (highest wins):

```
┌─────────────────────────────────────────────────────────┐
│  4. per-container env in catalog.yaml   (highest)       │
│  3. .toolbox/env.local   ← secrets, gitignored          │
│  2. .toolbox/env         ← shared config, committed     │
│  1. host environment     ← filtered by forward/deny     │
└─────────────────────────────────────────────────────────┘
```

```bash
# .toolbox/env  (commit this)
MY_APP_ENV=production
LOG_LEVEL=info

# .toolbox/env.local  (never commits — auto-gitignored by toolbox init)
OPENAI_API_KEY=sk-...
DATABASE_URL=postgres://...
AWS_ACCESS_KEY_ID=...
```

Secrets reach your tools without any extra plumbing. They're forwarded only at exec time via `--env` flags — never baked into image layers, never logged.

---

## Container Images

All specialized images inherit from base, so pipes and multi-tool commands always work:

```
toolbox-base    python3.14  node22  uv  rg  fd  jq  duckdb  dasel  hurl  mlr  sd  grex  delta  comby  ast-grep  skim  watchexec  ouch  rga  usql  rar  git  curl  aria2  pnpm  tsx
                requests  beautifulsoup4  pandas  duckdb(py)  …
    ├── toolbox-browser   + playwright  chromium
    └── toolbox-media     + imagemagick  yt-dlp  pillow  cyberdrop-dl-patched
```

`python3 scrape.py | rg "error" | jq` works in the browser container because it has all base tools too.

| Image | What's inside |
|-------|--------------|
| `ghcr.io/nitzzzu/toolbox-base` | python3.14, node22, uv, rg, fd, jq, duckdb, dasel, hurl, mlr, sd, grex, delta, comby, ast-grep, skim, watchexec, ouch, rga, usql, rar/unrar, git, curl, aria2, pnpm, typescript, tsx, requests, beautifulsoup4, pandas, duckdb (Python) |
| `ghcr.io/nitzzzu/toolbox-browser` | + playwright, chromium |
| `ghcr.io/nitzzzu/toolbox-media` | + imagemagick, yt-dlp, pillow, cyberdrop-dl-patched |

---

## Commands

### Core
```
toolbox exec [--container <n>] [--timeout <dur>] [--ephemeral] <cmd>
                       Route and run a command
toolbox shell [<container>]
                       Interactive shell in a container
```

### Lifecycle
```
toolbox init           Initialize .toolbox/ in current directory
toolbox up             Pull images and start all containers
toolbox down           Stop and remove all containers
toolbox pull           Pull latest images (no start)
toolbox restart [<n>]  Restart one or all containers
toolbox status         Show running containers
```

### Catalog
```
toolbox catalog list      List containers and their handles
toolbox catalog validate  Validate catalog.yaml syntax
```

### Environment
```
toolbox env list              Show what will be forwarded to containers
toolbox env set KEY=VALUE     Set a workspace env var
toolbox env unset KEY         Remove a var
```

### Observability
```
toolbox log [--tail N] [--json]
                       Show exec history (default: last 50 entries)
toolbox serve [--port N]
                       Start HTTP API server (default port: 7070)
```

---

## Workspace Layout

```
my-project/
└── .toolbox/
    ├── catalog.yaml    ← container definitions  (commit this ✓)
    ├── .gitignore      ← auto-generated, covers env.local
    ├── env             ← shared non-secret config  (commit this ✓)
    ├── env.local       ← secrets, never committed  (gitignored ✓)
    └── exec.log        ← exec history JSONL  (gitignored recommended)
```

`toolbox init` creates all of this. `toolbox up` gets containers running.

---

## SSH Backend

Run containers on a remote host — useful for GPU servers, Windows/Mac devs targeting Linux, or shared team environments:

```yaml
# .toolbox/catalog.yaml
runtime: ssh
ssh:
  host: user@myserver.com
  identity: ~/.ssh/toolbox_key
```

Toolbox SSHes in and runs `podman exec` remotely. Same catalog, same routing, execution happens elsewhere. All features (limits, network, timeout, ephemeral) work over SSH.

---

## Container Naming & Isolation

Containers are workspace-scoped: `toolbox-{project}-{hash}-{slot}`

Two agents on two different projects run simultaneously without collision:

```
toolbox-my-app-a1b2c3-base
toolbox-my-app-a1b2c3-browser
toolbox-other-project-d4e5f6-base
```

---

## Implementation Notes

- **Language:** Go — single binary, no runtime, cross-platform. ~2MB stripped.
- **Zero external dependencies** — stdlib + `gopkg.in/yaml.v3` only.
- **Podman preferred** — rootless by default, `--userns=keep-id` so files written in containers appear owned by your host user.
- **Runtime auto-detection** — checks `TOOLBOX_RUNTIME` env → catalog `runtime:` field → `podman` → `docker`.
- **Exit codes preserved** — `sys.exit(42)` in a container → `toolbox exec` exits 42.
- **Timeout precision** — uses `context.WithTimeout` + `exec.CommandContext` for clean process termination on expiry.
- **Exec log** — append-only JSONL at `.toolbox/exec.log`; every exec is recorded with timestamp, container, image, command, exit code, and duration.
- **HTTP API** — binds to `127.0.0.1` only by default; never exposed to the network without explicit routing.
- **Workspace file API** — `/workspace` CRUD, `/find` glob search, `/grep` content search all run directly on the host filesystem. No container required, no external tools (`rg`, `fd`) needed on the host.
- **Path traversal protection** — all workspace paths are validated against the workspace root; `../` escapes return 400.

---

## Build Plan

- [x] Phase 1 — Core execution: `exec`, `init`, `up`, `down`, env forwarding, auto-routing
- [x] Phase 2 — Catalog routing: multi-container, `handles[]` matching, `--container` flag
- [x] Phase 3 — Isolation & limits: resource limits, network isolation, exec timeout, ephemeral exec
- [x] Phase 4 — Observability: structured exec log (`toolbox log`), HTTP API (`toolbox serve`)
- [x] Phase 5 — Workspace API: file CRUD (`/workspace`), glob search (`/find`), content search (`/grep`), line-ranged reads
- [ ] Phase 6 — Polish: SSH backend binaries, community catalog, `toolbox shell` improvements
- [ ] Phase 7 — Ecosystem: `toolbox image init/build/push`, OCI label auto-discovery

---

<div align="center">

MIT License · Built with Go · No runtime dependencies · Works with Podman and Docker

</div>
