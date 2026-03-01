# toolbox

Container-native tool execution for AI agents. Route agent commands to the right container automatically — no MCP, no protocol overhead, no framework lock-in.

```
agent calls bash("playwright screenshot https://example.com")
    ↓
toolbox routes to browser container
    ↓
result streams back
```

## Install

```bash
# Linux / macOS
curl -fsSL https://github.com/toolbox-tools/toolbox/releases/latest/download/toolbox-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') -o /usr/local/bin/toolbox
chmod +x /usr/local/bin/toolbox

# Or build from source (requires Go 1.22+)
go install github.com/toolbox-tools/toolbox/cmd/toolbox@latest
```

Requires Podman (preferred) or Docker.

## Quickstart

```bash
# 1. Initialize your project
cd my-project
toolbox init

# 2. Edit .toolbox/catalog.yaml to add the containers you need
# (base container is included by default)

# 3. Add your API keys and secrets
echo "OPENAI_API_KEY=sk-..." >> .toolbox/env.local

# 4. Start containers
toolbox up

# 5. Run commands
toolbox exec "python3 train.py --epochs 100"
toolbox exec "playwright screenshot https://example.com out.png"
toolbox exec "duckdb data.parquet 'SELECT count(*) FROM data'"
```

## How It Works

### Catalog-Driven Routing

Each workspace has a `.toolbox/catalog.yaml` that declares the available containers and what commands each one handles:

```yaml
# .toolbox/catalog.yaml
version: 1

env:
  forward: ["OPENAI_*", "ANTHROPIC_*", "AWS_*"]
  deny: ["HOME", "USER", "SHELL"]

containers:
  base:
    image: ghcr.io/toolbox-tools/base:latest
    fallback: true  # catches everything not handled elsewhere

  browser:
    image: ghcr.io/toolbox-tools/browser:latest
    handles: [playwright, crawl4ai, chromium]

  data:
    image: ghcr.io/toolbox-tools/data:latest
    handles: [duckdb, polars, jupyter]
```

When you run `toolbox exec "playwright screenshot ..."`, toolbox:
1. Extracts `playwright` as the primary tool
2. Scans `handles[]` across all containers
3. Routes to the `browser` container
4. Lazy-starts it if not running
5. Streams output back

Everything else falls back to `base`.

### Container Image Hierarchy

All specialized images extend base, so pipes and multi-tool commands always work:

```
toolbox-base  (python3, node, rg, jq, curl, git, ffmpeg, ...)
  ├── toolbox-browser  (+ playwright, crawl4ai, chromium)
  ├── toolbox-data     (+ duckdb, polars, pandas, jupyter)
  └── toolbox-media    (+ imagemagick, yt-dlp, pillow)
```

`python3 scrape.py | rg "error" | jq` works in the browser container because it has all the base tools too.

### Workspace Volume

Your project directory is mounted as `/workspace` in every container. Files written inside appear on the host instantly — no copying, no syncing. With Podman's `--userns=keep-id`, files are owned by your host user.

### Environment Variable Forwarding

Three-layer merge, highest priority wins:

```
1. Per-container env in catalog.yaml   ← container-specific config
2. .toolbox/env.local                  ← gitignored secrets (OPENAI_API_KEY, etc.)
3. .toolbox/env                        ← committed shared config
4. Host environment                    ← filtered by forward/deny rules
```

Secrets in `.toolbox/env.local` never touch git. They're forwarded only at exec time.

## Commands

### Core

```bash
# Route a command to the right container
toolbox exec "python3 train.py"

# Force a specific container
toolbox exec --container browser "playwright screenshot https://example.com"

# Open an interactive shell
toolbox shell
toolbox shell browser
```

### Lifecycle

```bash
toolbox init          # Create .toolbox/ in current directory
toolbox up            # Pull images and start all containers
toolbox down          # Stop and remove all containers
toolbox pull          # Pull latest images (no start)
toolbox restart       # Restart all containers
toolbox restart base  # Restart one container
toolbox status        # Show running containers
```

### Catalog

```bash
toolbox catalog list      # Show containers and their handles
toolbox catalog validate  # Validate catalog.yaml syntax
```

### Environment

```bash
toolbox env list              # Show what will be forwarded to containers
toolbox env set KEY=VALUE     # Set a workspace env var (secrets auto-route to env.local)
toolbox env unset KEY         # Remove a var
```

## Agent Framework Integration

Wrap `toolbox exec` as your agent's bash tool. Four lines:

```python
# Python (agno, LangChain, custom loop)
import subprocess

def bash(cmd: str) -> str:
    r = subprocess.run(["toolbox", "exec", cmd], capture_output=True, text=True)
    return r.stdout + r.stderr
```

```typescript
// TypeScript (pi-mono, Claude Code, custom loop)
import { execa } from "execa";

async function bash(command: string): Promise<string> {
  const { stdout, stderr } = await execa("toolbox", ["exec", command]);
  return stdout + stderr;
}
```

The agent calls `bash("playwright screenshot ...")` exactly as before. Toolbox handles routing, isolation, and env forwarding transparently.

For **pi-mono** specifically, use the `toolbox.ts` extension which overrides all four core tools (bash, read, write, edit) to run through containers.

## Available Images

| Image | Contains | Handles |
|-------|----------|---------|
| `ghcr.io/toolbox-tools/base:latest` | python3, node, rg, jq, curl, git, ffmpeg | (fallback) |
| `ghcr.io/toolbox-tools/browser:latest` | playwright, crawl4ai, chromium | playwright, crawl4ai, chromium |
| `ghcr.io/toolbox-tools/data:latest` | duckdb, polars, pandas, jupyter | duckdb, polars, jupyter |
| `ghcr.io/toolbox-tools/media:latest` | imagemagick, yt-dlp, pillow | yt-dlp, convert, mogrify |

## SSH Backend

Run containers on a remote host:

```yaml
# .toolbox/catalog.yaml
runtime: ssh
ssh:
  host: user@myserver.com
  identity: ~/.ssh/toolbox_key
```

Toolbox SSHes into the remote, runs `podman exec` there. Useful for GPU servers, Windows/Mac developers targeting Linux, or shared team environments.

## Workspace Files

```
my-project/
└── .toolbox/
    ├── catalog.yaml    ← container definitions (commit this)
    ├── .gitignore      ← auto-generated, ignores env.local
    ├── env             ← shared non-secret config (commit this)
    └── env.local       ← secrets, never committed (gitignored)
```

## Container Naming

Containers are scoped to their workspace: `toolbox-{project}-{slot}`

Running two agents on two different projects simultaneously works without any configuration — their containers don't collide.

## Flags

```
--workspace <path>    Override workspace root (default: walk up from cwd looking for .toolbox/)
--version             Print version
--help                Print help

TOOLBOX_RUNTIME       Override runtime: podman|docker|ssh
TOOLBOX_WORKSPACE     Override workspace root
```
