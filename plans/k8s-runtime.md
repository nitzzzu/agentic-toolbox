# Plan: Kubernetes Runtime for toolbox

## Context

toolbox currently runs as a local CLI. The goal is to deploy `toolbox serve` as a k3s pod,
so agents running inside the cluster can call the HTTP API. Container spawning stays with
**system podman on the k3s host node** — toolbox reaches podman via the host's Unix socket
mounted into the pod.

## Architecture

```
[Agent pods in cluster]
        ↓  HTTP POST /exec  (ClusterIP DNS: toolbox.toolbox.svc:7070)
[toolbox Service → toolbox Deployment pod]
        ↓  calls `podman` CLI via CONTAINER_HOST=unix:///run/podman/podman.sock
[System podman on k3s host node]
        ↓  spawns catalog containers
[e.g. ghcr.io/nitzzzu/base with -v /var/toolbox/workspace:/workspace]
```

## Two Bugs to Fix First (minimal, targeted)

### 1. `internal/serve/serve.go:218` — hardcoded `127.0.0.1`

```go
// Current: unreachable from outside the pod
addr := fmt.Sprintf("127.0.0.1:%d", port)

// Fix: read TOOLBOX_HOST env var, default to 127.0.0.1 for local use
host := os.Getenv("TOOLBOX_HOST")
if host == "" {
    host = "127.0.0.1"
}
addr := fmt.Sprintf("%s:%d", host, port)
```

### 2. `internal/container/podman.go:30` — `--userns=keep-id` is rootless-only

System podman (via socket) runs as root. `--userns=keep-id` errors when uid=0.

```go
// Current: always appended
args = append(args, "--userns=keep-id")

// Fix: only for rootless
if os.Getuid() != 0 {
    args = append(args, "--userns=keep-id")
}
```

Same fix needed in `RunEphemeral()` (podman.go:79).

## Files to Create

### `Dockerfile.server` (multi-stage)
- Stage 1: `golang:1.22-alpine` — compiles `toolbox` binary with `CGO_ENABLED=0`
- Stage 2: `debian:bookworm-slim` — installs `podman` (no daemon needed, just CLI)
- Copies the binary, sets entrypoint to `toolbox serve`

### `deploy/k8s/namespace.yaml`
Namespace: `toolbox`

### `deploy/k8s/rbac.yaml`
- `ServiceAccount` named `toolbox` in namespace `toolbox`
- No ClusterRole needed — toolbox manages containers via podman, not the k8s API
- Note: if a k8s runtime is added later, extend here

### `deploy/k8s/configmap.yaml`
Mounts `catalog.yaml` from a ConfigMap so users can edit it without rebuilding the image.
Mounted to `/var/toolbox/workspace/.toolbox/catalog.yaml`.

### `deploy/k8s/deployment.yaml`
Key settings:
- `nodeSelector: toolbox-node: "true"` — required for hostPath volumes
- Volume mounts:
  - `hostPath: /run/podman/podman.sock` → `/run/podman/podman.sock` (type: Socket)
  - `hostPath: /var/toolbox/workspace` → `/var/toolbox/workspace` (SAME PATH — see Challenge 1)
  - ConfigMap → `/var/toolbox/workspace/.toolbox/catalog.yaml`
- Env vars:
  - `TOOLBOX_HOST=0.0.0.0`
  - `TOOLBOX_WORKSPACE=/var/toolbox/workspace`
  - `TOOLBOX_RUNTIME=podman`
  - `CONTAINER_HOST=unix:///run/podman/podman.sock`
- **initContainer** runs `toolbox up` to pull images and start containers on pod boot
- `livenessProbe: GET /health :7070`

### `deploy/k8s/service.yaml`
- Type: `ClusterIP`
- Port: 7070
- In-cluster DNS: `http://toolbox.toolbox.svc.cluster.local:7070`

## One-Time Host Setup (k3s node)

```bash
# Install podman
apt install -y podman          # Debian/Ubuntu
# or: dnf install podman       # RHEL/Fedora

# Enable system socket (creates /run/podman/podman.sock)
systemctl enable --now podman.socket

# Create workspace dir — same path used inside the pod
mkdir -p /var/toolbox/workspace
chmod 755 /var/toolbox/workspace

# Label your node so the deployment can pin to it
kubectl label node <your-node-name> toolbox-node=true
```

## Challenges / Self-Critique

### Challenge 1: Path identity is a hard constraint
The workspace hostPath **must** be mounted at the exact same absolute path inside the pod
(`/var/toolbox/workspace`) as it exists on the host. Reason: when toolbox spawns a catalog
container, it passes the *pod's* view of the workspace path to podman. Podman runs on the
host and interprets that as a host path. If they differ, mounts fail silently.

**Mitigation**: Use `/var/toolbox/workspace` as the canonical path in all manifests and host
setup. Document clearly.

### Challenge 2: Podman socket = root access on the host
Mounting the system podman socket gives the toolbox pod full container control over the
node. This is the same security tradeoff as mounting `/var/run/docker.sock`.

**Mitigation**: Acceptable for a personal homelab k3s. Not suitable for multi-tenant
clusters. Document clearly.

### Challenge 3: Stale podman containers on pod restart
If the toolbox pod crashes and restarts, old catalog containers from the previous run exist
on the host with the same names. `toolbox up` handles this by calling `podman rm --force`
before recreating. The initContainer approach ensures this runs on every pod startup.

### Challenge 4: catalog.yaml in a ConfigMap is not hot-reloaded
toolbox reads `catalog.yaml` at startup. Changes to the ConfigMap require a pod restart:

```bash
kubectl rollout restart deployment/toolbox -n toolbox
```

This is acceptable behavior.

### Challenge 5: Is k8s even necessary here?
The simpler alternative: run `toolbox serve` directly on the k3s host as a systemd service.
Agents outside the cluster would reach it via the node IP. This avoids all socket/path
identity complexity.

**Why k8s anyway**: Proper lifecycle management, health probes, ClusterIP DNS for in-cluster
agents, fits the existing k3s infrastructure pattern. Worth the small extra complexity.

## Verification

```bash
# 1. Host connectivity (add NodePort temporarily for testing)
curl http://<node-ip>:<nodeport>/health

# 2. In-cluster connectivity
kubectl run -it --rm test --image=alpine --restart=Never -- \
  wget -qO- http://toolbox.toolbox.svc:7070/health

# 3. Exec test from inside cluster
kubectl run -it --rm test --image=alpine --restart=Never -- \
  wget -qO- --post-data='{"cmd":"echo hello"}' \
  http://toolbox.toolbox.svc:7070/exec

# 4. Container status
kubectl exec -n toolbox deploy/toolbox -- toolbox status
```

## Files Modified
- `internal/serve/serve.go` — TOOLBOX_HOST env var
- `internal/container/podman.go` — conditional `--userns=keep-id`

## Files Created
- `Dockerfile.server`
- `deploy/k8s/namespace.yaml`
- `deploy/k8s/rbac.yaml`
- `deploy/k8s/configmap.yaml`
- `deploy/k8s/deployment.yaml`
- `deploy/k8s/service.yaml`
