# Configuration Reference

All configurable parameters for the Agentic Tenancy platform.

---

## Orchestrator Environment Variables

| Name | Default | Description |
|------|---------|-------------|
| `DYNAMODB_TABLE` | `tenant-registry` | DynamoDB table name for tenant records |
| `DYNAMODB_ENDPOINT` | _(empty)_ | Custom DynamoDB endpoint (set for local dev, e.g. `http://localhost:8000`) |
| `REDIS_ADDR` | `localhost:6379` | Redis address (`host:port`) |
| `K8S_NAMESPACE` | `tenants` | Kubernetes namespace for all tenant resources |
| `S3_BUCKET` | `zeroclaw-tenant-state` | S3 bucket for tenant state persistence |
| `WARM_POOL_TARGET` | `10` | Number of warm pool replicas to maintain |
| `ZEROCLAW_IMAGE` | `zeroclaw:latest` | Full ECR image URI for ZeroClaw container |
| `KATA_RUNTIME_CLASS` | `kata-qemu` | Kubernetes RuntimeClass name for tenant pods |
| `ROUTER_PUBLIC_URL` | _(empty)_ | Public URL of the router (e.g. `https://zeroclaw-router.example.com`). When set, enables auto-webhook registration on tenant create/update. |
| `PORT` | `8080` | HTTP listen port |
| `POD_NAME` | _(from downward API)_ | Pod name, used for leader election identity |
| `LEADER_ELECTION_ID` | `orchestrator-{POD_NAME}` | Unique identity for leader election |
| `LOCAL_MODE` | `false` | Set to `true` or set `DYNAMODB_ENDPOINT` to enable local dev mode (k8s operations skipped) |
| `AWS_ACCESS_KEY_ID` | _(from IAM)_ | AWS credentials (only needed in local mode) |
| `AWS_SECRET_ACCESS_KEY` | _(from IAM)_ | AWS credentials (only needed in local mode) |

### Internal Constants (code-level)

| Name | Value | Description |
|------|-------|-------------|
| `WakeLockTTL` | 240s | Redis wake lock TTL — auto-expires if replica crashes during wake |
| `PodReadyWait` | 210s | Max time to wait for pod to become Running |
| Lifecycle check interval | 30s | How often the leader checks for idle tenants |
| Reconciler interval | 60s | How often the reconciler checks DynamoDB vs k8s |
| Warm pool reconcile interval | 30s | How often the warm pool manager checks the Deployment |

---

## Router Environment Variables

| Name | Default | Description |
|------|---------|-------------|
| `REDIS_ADDR` | `localhost:6379` | Redis address (`host:port`) |
| `ORCHESTRATOR_ADDR` | `http://localhost:8080` | Orchestrator service URL (in-cluster: `http://orchestrator.tenants.svc.cluster.local:8080`) |
| `PUBLIC_BASE_URL` | `https://<YOUR_ROUTER_DOMAIN>` | Public URL for Telegram webhook registration |
| `PORT` | `9090` | HTTP listen port |

### Internal Constants (code-level)

| Name | Value | Description |
|------|-------|-------------|
| `endpointCacheTTL` | 5 min | Redis cache TTL for pod IP entries |
| `podReadyWait` | 5 min | Max wait for pod wake (includes Karpenter cold start) |
| HTTP client timeout | 320s | Must exceed podReadyWait + LLM response time |

---

## ZeroClaw Container Configuration

ZeroClaw pods are created dynamically by the orchestrator. Key pod spec fields:

| Field | Value | Notes |
|-------|-------|-------|
| `runtimeClassName` | `kata-qemu` | VM-level isolation |
| `priorityClassName` | `tenant-normal` | Higher priority than warm pool |
| `serviceAccountName` | `zeroclaw-tenant` | Shared SA with Bedrock-only IAM |
| `nodeName` | _(warm pod's node or empty)_ | Pinned when warm pool hit |
| `nodeSelector` | `katacontainers.io/kata-runtime: "true"` | Only schedule on kata nodes |
| `tolerations` | `kata-runtime=true:NoSchedule` | Tolerates kata node taint |
| `terminationGracePeriodSeconds` | 30 | Time for brain.db S3 flush |

### Container Environment Variables

| Name | Value | Description |
|------|-------|-------------|
| `TENANT_ID` | `{tenantID}` | Identifies the tenant |
| `TELEGRAM_BOT_TOKEN` | `{botToken}` | From DynamoDB record, passed at pod creation |

### Container Resources

| Resource | Request | Limit |
|----------|---------|-------|
| CPU | 100m | 500m |
| Memory | 384Mi | 512Mi |

### Volume Mounts

| Name | Mount Path | Type | Purpose |
|------|-----------|------|---------|
| `local-state` | `/zeroclaw-data` | emptyDir | Fast local SQLite I/O |
| `s3-state` | `/s3-state` | PVC (S3 CSI) | Persistent brain.db backup |

---

## Karpenter Configuration

### NodePool: `kata-metal`

| Field | Value | Description |
|-------|-------|-------------|
| `instance-category` | `c`, `m`, `r` | Compute, general, memory families |
| `instance-size` | `metal` | Bare metal only (required for Kata /dev/kvm) |
| `instance-generation` | `> 5` | Gen 6+ instances |
| `capacity-type` | `on-demand` | No spot (metal nodes are long-lived, warm pool sensitive) |
| `arch` | `amd64` | x86_64 only (arm64 metal lacks Kata CPU hotplug support) |
| `taint` | `kata-runtime=true:NoSchedule` | Isolates kata workloads |
| `consolidationPolicy` | `WhenEmpty` | Only consolidate when node has zero pods |
| `consolidateAfter` | `60s` | Wait before consolidating empty nodes |
| `expireAfter` | `720h` (30 days) | Max node lifetime |
| `cpu` limit | `256` | Maximum total vCPUs across all kata nodes |

### EC2NodeClass: `kata`

| Field | Value | Description |
|-------|-------|-------------|
| `amiSelectorTerms` | `al2023@latest` | Amazon Linux 2023 |
| `blockDeviceMappings` | 500Gi gp3, encrypted | Root volume |
| `role` | `<EKS_CLUSTER_NAME>` | Instance profile for node |
| `securityGroupSelectorTerms` | `karpenter.sh/discovery: <EKS_CLUSTER_NAME>` | Auto-discovered SGs |
| `subnetSelectorTerms` | `karpenter.sh/discovery: <EKS_CLUSTER_NAME>` | Auto-discovered subnets |
| `userData` | devmapper setup script | Creates thin-pool for kata-qemu snapshotter |

---

## DynamoDB Schema

### Table: `tenant-registry`

| Field | Type | Key | Description |
|-------|------|-----|-------------|
| `tenant_id` | String | **PK** (Hash) | Unique tenant identifier |
| `status` | String | — | `idle`, `running`, `provisioning`, `terminated` |
| `pod_name` | String | — | k8s pod name (e.g. `zeroclaw-alice`). Empty when idle. |
| `pod_ip` | String | — | Pod cluster IP. Empty when idle. |
| `namespace` | String | — | k8s namespace (always `tenants`) |
| `s3_prefix` | String | — | S3 key prefix (e.g. `tenants/alice/`) |
| `bot_token` | String | — | Telegram Bot API token. Redacted from public API responses. |
| `created_at` | String (RFC3339) | — | Tenant creation timestamp |
| `last_active_at` | String (RFC3339) | — | Last message activity timestamp |
| `idle_timeout_s` | Number | — | Idle timeout in seconds (default: 300) |

### Billing Mode

PAY_PER_REQUEST (on-demand). No provisioned capacity needed at current scale.

---

## Redis Key Schema

| Key Pattern | TTL | Purpose |
|-------------|-----|---------|
| `router:endpoint:{tenantID}` | 5 min | Cached pod IP for the router to skip orchestrator lookup |
| `tenant:waking:{tenantID}` | 240s | Distributed wake lock — prevents duplicate pod creation |

### Notes

- The router sets `router:endpoint:{tenantID}` after a successful wake
- The orchestrator clears `router:endpoint:{tenantID}` on tenant deletion and during reconciliation (when pod is missing)
- The wake lock `tenant:waking:{tenantID}` is set with `SET NX EX` (atomic acquire) and deleted after wake completes (or expires on crash)
- No other Redis keys are used — Redis is purely a cache/lock store
