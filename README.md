# smolvm Dagger Module

A [Dagger](https://dagger.io) module that provides **microVM-based sandboxed execution** via [smolvm](https://github.com/smol-machines/smolvm).

Unlike Dagger's built-in container execution (runc/namespaces), this module runs workloads in real virtual machines using hardware virtualization (KVM on Linux, Hypervisor.framework on macOS). This provides:

- **Hardware-level isolation** — separate kernel per workload, not shared namespaces
- **Hostname-based egress filtering** — restrict outbound network to specific domains
- **No privileged containers needed** — smolvm uses the host's hypervisor directly

## Install

```bash
dagger install github.com/smol-machines/dagger-smolvm
```

## Prerequisites

[smolvm](https://github.com/smol-machines/smolvm) installed and the API server running:

```bash
smolvm serve start
```

## Usage

### One-shot execution

```bash
# Run a command in a microVM
dagger -m github.com/smol-machines/dagger-smolvm call \
  exec --image python:3.12-alpine --command python,-c,"print('hello from a VM')"

# Run code directly
dagger -m github.com/smol-machines/dagger-smolvm call \
  run-code --code "console.log(2 + 2)" --language node

# Check smolvm server connectivity
dagger -m github.com/smol-machines/dagger-smolvm call health
```

### Network egress filtering

```bash
# Only allow requests to OpenAI and Anthropic APIs
dagger -m github.com/smol-machines/dagger-smolvm call \
  with-egress-filter --hosts api.openai.com,api.anthropic.com \
  exec --image python:3.12-alpine \
       --command python,-c,"import urllib.request; print(urllib.request.urlopen('https://api.openai.com').status)"
```

### Custom resources

```bash
dagger -m github.com/smol-machines/dagger-smolvm call \
  with-resources --cpus 4 --memory-mb 2048 \
  exec --image gcc:alpine --command gcc,--version
```

### Persistent machine (multi-step workflow)

```bash
# Create a persistent VM and install packages
dagger -m github.com/smol-machines/dagger-smolvm call \
  machine --name dev --image python:3.12-alpine \
  exec --command pip,install,requests

# Run more commands in the same VM
dagger -m github.com/smol-machines/dagger-smolvm call \
  machine --name dev --image python:3.12-alpine \
  exec --command python,-c,"import requests; print(requests.get('https://httpbin.org/get').status_code)"
```

### From another Dagger module (Go)

```go
func (m *MyModule) SecureTest(ctx context.Context, src *dagger.Directory) (string, error) {
    return dag.Smolvm().
        WithEgressFilter([]string{"registry.npmjs.org"}).
        WithResources(2, 1024).
        Exec(ctx, "node:22-alpine", []string{"npm", "test"}, dagger.SmolvmExecOpts{
            Env: []string{"NODE_ENV=test"},
        })
}
```

### From another Dagger module (Python)

```python
@function
async def secure_test(self, src: dagger.Directory) -> str:
    return await (
        dag.smolvm()
        .with_egress_filter(["registry.npmjs.org"])
        .with_resources(cpus=2, memory_mb=1024)
        .exec("node:22-alpine", ["npm", "test"], env=["NODE_ENV=test"])
    )
```

## Architecture

```
┌─────────────────────────────────┐
│  Dagger Engine (container)      │
│                                 │
│  ┌───────────────────────────┐  │
│  │  smolvm Dagger Module     │  │
│  │  (this code — HTTP client)│  │
│  └───────────┬───────────────┘  │
│              │ HTTP              │
└──────────────┼──────────────────┘
               │
    ┌──────────▼──────────┐
    │  smolvm serve       │  ← Host machine
    │  (API server :8080) │
    └──────────┬──────────┘
               │
    ┌──────────▼──────────┐
    │  microVM (libkrun)  │  ← Real hardware virtualization
    │  ┌────────────────┐ │
    │  │ OCI container  │ │
    │  │ (your command) │ │
    │  └────────────────┘ │
    └─────────────────────┘
```

## Container vs microVM execution

| Property | Dagger (containers) | smolvm (microVMs) |
|---|---|---|
| Isolation | Kernel namespaces + seccomp | Separate kernel (hardware) |
| Network filtering | IP/CIDR only | Hostname + CIDR |
| Privilege required | Docker daemon / rootless | Hypervisor.framework (macOS) |
| Startup overhead | ~50ms | ~300ms |
| Use case | Trusted CI/CD pipelines | Untrusted code, AI agents, compliance |
