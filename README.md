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
```

---

## The Catalog

Each workspace has a `.toolbox/catalog.yaml` that declares available containers and what commands each handles:

```yaml
# .toolbox/catalog.yaml
version: 1

env:
  forward: ["OPENAI_*", "ANTHROPIC_*", "AWS_*", "DATABASE_*"]
  deny:    ["HOME", "USER", "SHELL"]

containers:
  base:
    image: ghcr.io/nitzzzu/toolbox-base:latest
    description: "python3, node, rg, jq, curl, git, ffmpeg"
    fallback: true                          # catches everything unmatched

  browser:
    image: ghcr.io/nitzzzu/toolbox-browser:latest
    handles: [playwright, crawl4ai, chromium]
    env:
      PLAYWRIGHT_BROWSERS_PATH: /ms-playwright

  data:
    image: ghcr.io/nitzzzu/toolbox-data:latest
    handles: [duckdb, polars, jupyter]
    env:
      DUCKDB_MEMORY_LIMIT: "4GB"
```

**Routing** is first-token based: `playwright screenshot ...` → extract `playwright` → match `handles[]` → route to `browser`. Everything else hits the `fallback`.

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
toolbox-base    python3  node  rg  jq  curl  git  ffmpeg  …
    ├── toolbox-browser   + playwright  crawl4ai  chromium
    ├── toolbox-data      + duckdb  polars  pandas  jupyter
    └── toolbox-media     + imagemagick  yt-dlp  pillow
```

`python3 scrape.py | rg "error" | jq` works in the browser container because it has all base tools too.

| Image | What's inside |
|-------|--------------|
| `ghcr.io/nitzzzu/toolbox-base` | python3, node, rg, jq, curl, git, ffmpeg, fd, bash |
| `ghcr.io/nitzzzu/toolbox-browser` | + playwright, crawl4ai, chromium, beautifulsoup4 |
| `ghcr.io/nitzzzu/toolbox-data` | + duckdb, polars, pandas, numpy, scikit-learn, jupyter |
| `ghcr.io/nitzzzu/toolbox-media` | + imagemagick, yt-dlp, pillow, moviepy |

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
toolbox exec [--container <n>] <cmd>   Route and run a command
toolbox shell [<container>]               Interactive shell in a container
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

---

## Workspace Layout

```
my-project/
└── .toolbox/
    ├── catalog.yaml    ← container definitions  (commit this ✓)
    ├── .gitignore      ← auto-generated, covers env.local
    ├── env             ← shared non-secret config  (commit this ✓)
    └── env.local       ← secrets, never committed  (gitignored ✓)
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

Toolbox SSHes in and runs `podman exec` remotely. Same catalog, same routing, execution happens elsewhere.

---

## Container Naming & Isolation

Containers are workspace-scoped: `toolbox-{project}-{slot}`

Two agents on two different projects run simultaneously without collision:

```
toolbox-my-app-base
toolbox-my-app-browser
toolbox-other-project-base
```

---

## Implementation Notes

- **Language:** Go — single binary, no runtime, cross-platform. 2MB stripped.
- **Zero external dependencies** — stdlib only. Includes a hand-rolled YAML parser.
- **Podman preferred** — rootless by default, `--userns=keep-id` so files written in containers appear owned by your host user
- **Runtime auto-detection** — checks `TOOLBOX_RUNTIME` env → catalog `runtime:` field → `podman` → `docker`
- **Exit codes preserved** — `sys.exit(42)` in a container → `toolbox exec` exits 42

---

## Build Plan

- [x] Phase 1 — Core execution: `exec`, `init`, `up`, `down`, env forwarding, auto-routing
- [x] Phase 2 — Catalog routing: multi-container, `handles[]` matching, `--container` flag
- [ ] Phase 3 — Polish: SSH backend binaries, community catalog, `toolbox shell` improvements
- [ ] Phase 4 — Ecosystem: `toolbox image init/build/push`, OCI label auto-discovery

---

<div align="center">

MIT License · Built with Go · No runtime dependencies · Works with Podman and Docker

</div>
