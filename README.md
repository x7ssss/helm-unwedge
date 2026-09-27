# helm-unwedge

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)

Production-grade, zero-dependency Go CLI for safely diagnosing and unlocking deadlocked Helm v3 releases (`pending-upgrade`, `pending-install`, `pending-rollback`) without importing the bloated Helm SDK.

---

## The Problem

When a Helm deployment job crashes, times out, or gets killed mid-flight, Helm leaves the release stuck in transition states:
- `pending-upgrade`
- `pending-install`
- `pending-rollback`

Subsequent runs fail with the classic Helm error:
```
Error: UPGRADE FAILED: another operation (install/upgrade/rollback) is in progress
```

Resolving this typically requires manual, dangerous `kubectl delete secret` interventions or pulling in the entire ~100MB Helm SDK dependency tree.

`helm-unwedge` solves this cleanly and surgically by directly understanding Helm v3 storage wire protocols using standard Go libraries and official Kubernetes `client-go`.

---

## Architectural Invariants

### 1. Zero Helm SDK Invariant
`helm-unwedge` intentionally avoids importing `helm.sh/helm/v3`. The Helm SDK introduces hundreds of transitive dependencies (Docker, Kustomize, Sprig, OCI registries), swelling binary size beyond 80MB.

By leveraging:
- Go standard library (`encoding/base64`, `compress/gzip`, `encoding/json`)
- `k8s.io/client-go v0.32.0`
- `k8s.io/apimachinery v0.32.0`
- `github.com/spf13/cobra v1.8.1`

The resulting release binary achieves a compact distribution footprint (~12MB compressed).

### 2. Dual-Mutation Invariant
Helm v3 release state is split between:
1. **Kubernetes Secret Labels**: `status` and `modifiedAt` used by Helm for listing, filtering, and state machines.
2. **Secret Payload (`.data.release`)**: Gzip-compressed, base64-encoded JSON containing `.info.status` and `.info.description`.

Patching only the JSON payload leaves `helm list` seeing the old pending state. Patching only the Secret label leaves release operations seeing mismatched internal state.

`helm-unwedge` enforces an atomic dual-mutation PATCH/PUT that updates both layers in a single atomic Kubernetes API transaction.

### 3. Distributed Mutual Exclusion
To prevent split-brain mutations during concurrent CI/CD pipeline runs, `helm-unwedge` acquires a distributed lease (`coordination.k8s.io/v1` Lease named `helm-lock-<release>`) in the target namespace with a 15-second duration and automated 5-second background renewals before mutating. The lock is cleanly deleted on exit.

### 4. Stale Heuristics
Before unlocking, `helm-unwedge` verifies whether the release is truly abandoned. If `.labels.modifiedAt` or `.info.last_deployed` was updated within `--stale-after` (default 10m), the command aborts to avoid interrupting an active deployment, unless `--force` is explicitly provided.

### 5. Revision 1 Handling
If revision 1 is stuck in `pending-install`, `helm-unwedge` provides two strategies:
- `--strategy=mark-failed` (recommended): Marks revision 1 as failed. Subsequent `helm upgrade --install` commands detect an existing release and safely perform a 3-way merge upgrade.
- `--strategy=purge-v1`: Deletes the revision 1 secret with an explicit warning, allowing a clean initial install.

---

## CLI Reference

### 1. `analyze` (Read-Only Audit)
Diagnoses the state of a release without making any changes.

```bash
helm-unwedge analyze my-release -n production
```

Output includes:
- Secret name and revision
- Status alignment between Secret labels and internal JSON payload
- Staleness status and elapsed time
- Active `coordination.k8s.io` lease locks
- Prescribed remedy

### 2. `heal` (Surgical Unlocker)
Acquires distributed lock, verifies stale threshold, and applies atomic dual-mutation.

```bash
# Unlock an abandoned pending release
helm-unwedge heal my-release -n production

# Override staleness threshold if you know the job was killed
helm-unwedge heal my-release -n production --force

# Dry-run inspection
helm-unwedge heal my-release -n production --dry-run

# Purge stuck revision 1 (pending-install)
helm-unwedge heal my-release -n production --strategy=purge-v1
```

### 3. `auto` (CI/CD Gatekeeper)
Engineered specifically for automated deployment pipelines (e.g. GitHub Actions, GitLab CI, ArgoCD pre-sync hooks).

```bash
helm-unwedge auto --release my-release -n production --stale-after 10m
```

Exit codes:
- `0`: Release is healthy OR was deadlocked and successfully unlocked.
- `1`: Active concurrent deployment detected (held lease lock or recent modification within stale threshold).

#### GitHub Actions Workflow Example

```yaml
- name: Unwedge Stale Helm Release
  run: |
    helm-unwedge auto --release ${{ env.RELEASE_NAME }} -n ${{ env.NAMESPACE }} --stale-after 10m

- name: Deploy Helm Chart
  run: |
    helm upgrade --install ${{ env.RELEASE_NAME }} ./charts/my-app -n ${{ env.NAMESPACE }}
```

### 4. `list-stuck` (Cluster Scanner)
Scans a namespace or the entire cluster for releases stuck in transition states.

```bash
# Scan specific namespace
helm-unwedge list-stuck -n production

# Scan all namespaces across the cluster
helm-unwedge list-stuck -A
```

---

## Installation & Building

### From Source

```bash
git clone https://github.com/x7ssss/helm-unwedge.git
cd helm-unwedge
make build
```

The compiled binary will be placed at `bin/helm-unwedge`.

### Running Tests

```bash
go test -v -count=1 ./...
```

### Cross-Compilation

To generate binaries for Linux, macOS, and Windows:

```bash
# On Unix / Linux:
make cross-compile

# On Windows PowerShell:
.\build.ps1
```

Compiled binaries and release archives will be generated in `dist/`.
