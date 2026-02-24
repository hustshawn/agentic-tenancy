# Agentic Tenancy

[![Version](https://img.shields.io/badge/version-v0.1.0-blue.svg)](CHANGELOG.md)
[![Go](https://img.shields.io/badge/go-1.24-00ADD8.svg)](go.mod)

Multi-tenant lifecycle management platform for AI agents on AWS EKS. Each tenant gets an isolated [Kata Containers](https://katacontainers.io/) pod running a [ZeroClaw](https://github.com/hustshawn/zeroclaw) agent instance with VM-level isolation, S3-backed session persistence, and automatic idle timeout. The system manages wake-on-demand, warm pool pre-provisioning, and distributed HA coordination so individual agents appear always-on to end users while consuming zero resources when idle.

> Status: **Working** — Tested on EKS with Karpenter + Kata Containers

---

## Architecture

```
Telegram
   │
   ▼
ALB (HTTPS)
   │
   ▼
Router (× 2)
   ├── Redis endpoint cache (5min TTL)
   ├── Auto-wake on miss → Orchestrator
   └── Forward message → ZeroClaw /webhook → reply via Telegram API
         │
         ▼
Orchestrator (× 2, HA)
   ├── DynamoDB — tenant registry
   ├── Redis — wake distributed lock
   ├── Karpenter — kata metal node provisioning
   ├── Lifecycle controller — idle timeout (leader election)
   ├── Reconciler — state drift detection (60s)
   └── Warm pool — pre-started ZeroClaw pods (Deployment)
         │
         ▼
zeroclaw-{tenantID} Pod (kata-qemu, amd64 metal)
   ├── emptyDir /zeroclaw-data — SQLite brain.db (fast local I/O)
   └── S3 CSI /s3-state — brain.db backup (persist across restarts)
```

---

## Components

| Component | Role | Location |
|-----------|------|----------|
| **Orchestrator** | Tenant CRUD, pod lifecycle (wake/idle/delete), warm pool, reconciler, leader-elected idle timeout | `cmd/orchestrator` |
| **Router** | Telegram webhook receiver, Redis pod-IP cache, wake-on-miss, message forwarding to ZeroClaw | `cmd/router` |
| **Registry** | DynamoDB-backed tenant state (status, pod_ip, bot_token, idle_timeout) | `internal/registry` |
| **Warm Pool** | Maintains a Deployment of pre-started low-priority ZeroClaw pods for fast wake (~13s vs 3-4min) | `internal/warmpool` |
| **Reconciler** | Every 60s, detects DynamoDB/k8s state drift; resets orphaned "running" tenants to "idle" | `internal/reconciler` |
| **Lifecycle** | Leader-elected idle timeout controller; terminates pods exceeding `idle_timeout_s` | `internal/lifecycle` |
| **Lock** | Redis-based distributed wake lock (`SET NX EX`) prevents duplicate pod creation across replicas | `internal/lock` |
| **K8s Client** | Creates tenant pods, PV/PVC (S3 CSI), warm pool Deployment; warm pod claim logic | `internal/k8s` |
| **Telegram** | Webhook registration/deletion helper via Telegram Bot API | `internal/telegram` |
| **ztm CLI** | Bash CLI for tenant management (wraps orchestrator/router APIs via kubectl exec or direct HTTP) | `scripts/ztm.sh` |

---

## Quick Start

### Prerequisites

- EKS cluster with Kata Containers runtime (`kata-qemu` RuntimeClass)
- Karpenter installed with `kata-metal` NodePool (see `deploy/02-karpenter.yaml`)
- S3 CSI Driver (Mountpoint for Amazon S3) add-on installed
- DynamoDB table `tenant-registry` created
- S3 bucket `zeroclaw-tenant-state` created
- Redis deployed in-cluster (`redis.tenants.svc.cluster.local:6379`)
- ECR repositories: `orchestrator`, `router`, `zeroclaw`

### Deploy

> **Important**: Before deploying, you must replace placeholders in the manifest files. See [deploy/README.md](deploy/README.md) for detailed configuration instructions.

```bash
# 1. Build and push all images
./scripts/build-and-deploy.sh all

# 2. Configure deployment manifests (replace placeholders)
# See deploy/README.md for detailed instructions
export AWS_ACCOUNT_ID="123456789012"
export AWS_REGION="us-west-2"
export YOUR_ROUTER_DOMAIN="router.example.com"
export EKS_CLUSTER_NAME="my-eks-cluster"

# Option A: Use envsubst to generate configured manifests
envsubst < deploy/01-orchestrator.yaml > /tmp/01-orchestrator-configured.yaml
envsubst < deploy/02-karpenter.yaml > /tmp/02-karpenter-configured.yaml

# Option B: Edit files directly (see deploy/README.md)

# 3. Apply prerequisites
kubectl apply -f deploy/00-prerequisites.yaml

# 4. Create Redis secret
kubectl create secret generic orchestrator-config \
  --namespace=tenants \
  --from-literal=redis-addr='redis.tenants.svc.cluster.local:6379'

# 5. Apply Karpenter NodePool + EC2NodeClass
kubectl apply -f /tmp/02-karpenter-configured.yaml

# 6. Deploy orchestrator + router
kubectl apply -f /tmp/01-orchestrator-configured.yaml

# 7. Verify health
kubectl -n tenants get pods
kubectl -n tenants logs deployment/orchestrator --tail=20
kubectl -n tenants logs deployment/router --tail=20

# 8. Create your first tenant
ztm tenant create alice "<BOT_TOKEN>" --idle-timeout 3600

# 9. Register Telegram webhook
ztm webhook register alice

# 10. Send a message to @YourBot on Telegram — it should wake and reply
```

---

### CLI Installation

The `ztm` CLI is a Go binary that manages tenant operations. It requires `kubectl` and a valid kubeconfig.

```bash
# Build from source
make ztm

# Install to PATH
make install-ztm

# Verify installation
ztm version
```

### Using the CLI

```bash
# Create a tenant
ztm tenant create alice 1234567890:AAHxyz --idle-timeout 3600

# List tenants
ztm tenant list

# Get tenant details
ztm tenant get alice

# Update tenant
ztm tenant update alice --idle-timeout 1800

# Register webhook
ztm webhook register alice

# Delete tenant
ztm tenant delete alice

# Use different cluster
ztm tenant list --context prod-eks --namespace production

# JSON output
ztm tenant get alice --output json
```

See [docs/operations.md](docs/operations.md) for complete CLI reference.

---

## Infrastructure Reference

| Resource | Value |
|----------|-------|
| EKS cluster | `<EKS_CLUSTER_NAME>` (`<AWS_REGION>`) |
| Namespace | `tenants` (configurable via `K8S_NAMESPACE`) |
| DynamoDB table | `<DYNAMODB_TABLE>` (default: `tenant-registry`) |
| S3 bucket | `<S3_BUCKET>` (default: `zeroclaw-tenant-state`) |
| Redis | `<REDIS_ADDR>` (default: `redis.tenants.svc.cluster.local:6379`) |
| ECR registry | `<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com` |
| Karpenter NodePool | `kata-metal` (c/m/r family, `.metal` size, amd64, on-demand) |
| Kata runtime | `kata-qemu` (virtiofs for S3 CSI compatibility) |

### ECR Images

| Image | Description |
|-------|-------------|
| `<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com/orchestrator:latest` | Orchestrator (multi-arch) |
| `<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com/router:latest` | Router (multi-arch) |
| `<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com/zeroclaw:latest` | ZeroClaw agent (amd64 only) |

### Pod Identity Associations

| ServiceAccount | IAM Role | Permissions |
|----------------|----------|-------------|
| `orchestrator` | `orchestrator-pod-identity` | DynamoDB read/write |
| `zeroclaw-tenant` | `zeroclaw-tenant-pod-identity` | Bedrock InvokeModel |

---

## API Reference

### Orchestrator (`:8080`)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/tenants` | Create tenant (auto-registers webhook if `ROUTER_PUBLIC_URL` set) |
| `GET` | `/tenants` | List all tenants (BotToken redacted) |
| `GET` | `/tenants/:id` | Get tenant record (BotToken redacted) |
| `GET` | `/tenants/:id/bot_token` | Get bot token (internal, used by Router) |
| `PATCH` | `/tenants/:id` | Update `bot_token` and/or `idle_timeout_s` |
| `DELETE` | `/tenants/:id` | Delete tenant + pod + PVC + webhook |
| `PUT` | `/tenants/:id/activity` | Update `last_active_at` timestamp |
| `POST` | `/wake/:id` | Wake tenant pod, returns `{"pod_ip": "..."}` |
| `GET` | `/healthz` | Health check |

### Router (`:9090`)

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/tg/:tenantID` | Telegram webhook receiver |
| `POST` | `/admin/webhook/:tenantID` | Register Telegram webhook for tenant |
| `GET` | `/healthz` | Health check |

---

## Documentation

| Document | Contents |
|----------|----------|
| [docs/architecture.md](docs/architecture.md) | System flow, warm pool design, session persistence, state machine, HA, Karpenter, security |
| [docs/operations.md](docs/operations.md) | ztm CLI reference, tenant lifecycle, logs, build & deploy, troubleshooting |
| [docs/configuration.md](docs/configuration.md) | Env vars, DynamoDB schema, Redis keys, Karpenter config |
