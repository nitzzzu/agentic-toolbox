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

### Endpoints

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

### Agent integration via HTTP

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

## Agent Integration

Wrap `toolbox exec` as your agent's bash tool. Four lines:

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

The agent calls `bash("playwright screenshot ...")` exactly as before. Toolbox routes to the right container transparently.

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

---

## Build Plan

- [x] Phase 1 — Core execution: `exec`, `init`, `up`, `down`, env forwarding, auto-routing
- [x] Phase 2 — Catalog routing: multi-container, `handles[]` matching, `--container` flag
- [x] Phase 3 — Isolation & limits: resource limits, network isolation, exec timeout, ephemeral exec
- [x] Phase 4 — Observability: structured exec log (`toolbox log`), HTTP API (`toolbox serve`)
- [ ] Phase 5 — Polish: SSH backend binaries, community catalog, `toolbox shell` improvements
- [ ] Phase 6 — Ecosystem: `toolbox image init/build/push`, OCI label auto-discovery

---

<div align="center">

MIT License · Built with Go · No runtime dependencies · Works with Podman and Docker

</div>
