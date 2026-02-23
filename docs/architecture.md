# Architecture

Deep dive into the Agentic Tenancy system design, component interactions, and infrastructure decisions.

---

## System Flow: Telegram Message → Reply

```
User sends message to @TenantBot
         │
         ▼
Telegram servers ──POST /tg/{tenantID}──▶ ALB (HTTPS)
         │
         ▼
    Router (port 9090)
         │
         ├── 1. Read body, ack Telegram with 200 OK immediately
         │
         ├── 2. Check Redis cache: router:endpoint:{tenantID}
         │      │
         │      ├── HIT (pod_ip found) ──▶ skip to step 5
         │      │
         │      └── MISS ──▶ step 3
         │
         ├── 3. Send "⏳ Starting up..." to user via Telegram Bot API
         │      (fetches BotToken from Orchestrator GET /tenants/{id}/bot_token)
         │
         ├── 4. POST /wake/{tenantID} → Orchestrator
         │      │
         │      │  Orchestrator:
         │      │  a. Check DynamoDB — if status=running, return pod_ip immediately
         │      │  b. Acquire Redis wake lock: SET tenant:waking:{id} 1 NX EX 240
         │      │     - If lock held by another replica → poll DynamoDB until running
         │      │  c. Ensure S3 CSI PVC exists (idempotent create)
         │      │  d. Check warm pool for available pod (label warm=true, phase=Running)
         │      │     - HIT: detach warm pod (warm=true → warm=consuming), delete it,
         │      │       pin tenant pod to same node (skip Karpenter provisioning)
         │      │     - MISS: create tenant pod without node pinning (Karpenter cold start)
         │      │  e. Create zeroclaw-{tenantID} pod with kata-qemu runtime
         │      │  f. Poll until pod Running + has PodIP (up to 210s)
         │      │  g. Update DynamoDB: status=running, pod_name, pod_ip
         │      │  h. Release wake lock, return pod_ip
         │      │
         │      └── Router receives pod_ip, caches in Redis (5min TTL)
         │
         ├── 5. Forward to ZeroClaw:
         │      POST http://{pod_ip}:3000/webhook {"message": "<text>"}
         │      ← {"response": "<reply>"}
         │
         ├── 6. Send response to user via Telegram Bot API (sendMessage)
         │
         └── 7. PUT /tenants/{tenantID}/activity → update last_active_at
```

### Timing

| Scenario | Latency |
|----------|---------|
| Pod already running (cache hit) | ~2-5s (LLM response time) |
| Warm pool hit (node exists) | ~13-20s (pod create + LLM) |
| Cold start (Karpenter provisions node) | ~3-5min (metal node + pod + LLM) |

---

## Warm Pool Design

The warm pool eliminates the dominant cold-start cost (Karpenter metal node provisioning, 3-4min) by keeping pre-started ZeroClaw pods running on existing nodes.

### How It Works

```
warm-pool Deployment (replicas: WARM_POOL_TARGET)
   │
   ├── warm-pool-abc (Running, node=ip-10-0-1-1, labels: warm=true)
   ├── warm-pool-def (Running, node=ip-10-0-2-2, labels: warm=true)
   └── warm-pool-ghi (Pending, waiting for node)

On tenant wake:
   1. Orchestrator lists pods with labels app=warm-pool,warm=true
   2. Finds a Running pod with PodIP and no DeletionTimestamp
   3. Atomically changes label: warm=true → warm=consuming
      (Deployment selector requires warm=true, so pod is now orphaned)
   4. Deletes the warm pod to free node resources
   5. Creates tenant pod with nodeName pinned to the warm pod's node
   6. Deployment sees replica count dropped → creates replacement warm pod

If no warm pods available → standard cold start via Karpenter.
```

### Key Properties

- **Low priority**: Warm pods use PriorityClass `tenant-low` — they can be preempted by real tenant pods
- **Real ZeroClaw image**: Warm pods run the actual ZeroClaw container (not pause), so the image is pre-pulled on the node
- **Shared service account**: All warm/tenant pods use `zeroclaw-tenant` (Bedrock access only)
- **Automatic replenishment**: Kubernetes Deployment controller handles replacement — no custom logic needed
- **Reconcile loop**: The warm pool manager checks every 30s that the Deployment exists and has the correct replica count

---

## Session Persistence

### The Problem

SQLite (`brain.db`) requires full POSIX semantics (random writes, `flock`, `rename`). The S3 CSI driver (Mountpoint for Amazon S3) supports only sequential writes and does not support file locking or rename-over-existing.

### The Solution: Copy-on-Start / Copy-on-Shutdown

```
┌─────────────────────────────────┐     ┌──────────────────────────┐
│  emptyDir /zeroclaw-data        │     │  S3 CSI /s3-state        │
│  (fast local ephemeral storage) │     │  (persistent, durable)   │
│                                 │     │                          │
│  brain.db ← ZeroClaw reads/    │     │  brain.db (backup copy)  │
│              writes here        │     │                          │
└─────────────────────────────────┘     └──────────────────────────┘

On pod start (entrypoint):
  cp /s3-state/brain.db → /zeroclaw-data/workspace/memory/brain.db

During runtime:
  ZeroClaw reads/writes brain.db on emptyDir (full POSIX, fast)

On SIGTERM (entrypoint trap):
  cp /zeroclaw-data/workspace/memory/brain.db → /s3-state/brain.db
```

### Data Loss Window

If a pod crashes (OOM, node failure) without receiving SIGTERM, state since the last graceful shutdown is lost. This is acceptable because:
- Agent memory is append-only by nature — partial loss is tolerable
- `terminationGracePeriodSeconds: 30` gives ample time for the copy
- The reconciler detects crashed pods and resets state within 60s

### S3 CSI Configuration

Each tenant gets a dedicated PV/PVC pair:
- **PV**: `pv-tenant-{tenantID}`, CSI driver `s3.csi.aws.com`, bucket `zeroclaw-tenant-state`, subPath `tenants/{tenantID}`
- **PVC**: `pvc-tenant-{tenantID}`, StorageClass `s3-tenant-state`, bound to the PV

The PVC is created on first wake and retained on pod deletion (reclaim policy: Retain).

---

## State Machine

```
                  ┌──────────────────────────┐
                  │                          │
         create   ▼    wake (POST /wake)     │   idle timeout
   ────────▶  idle  ──────────────────▶ running ──────────────┐
                  ▲                          │                │
                  │                          │                │
                  │    pod deleted /          │                │
                  │    reconciler detects     │                │
                  │    missing pod            │                │
                  └──────────────────────────┘                │
                  ▲                                           │
                  │           lifecycle controller            │
                  └───────────────────────────────────────────┘

   provisioning: transient state during wake (DynamoDB only, not shown)
```

### Who Does What

| Component | Trigger | Action |
|-----------|---------|--------|
| **API handler** (wake) | `POST /wake/{id}` | idle → provisioning → running |
| **Lifecycle controller** | 30s tick (leader only) | running → idle (if `now - last_active_at > idle_timeout_s`) |
| **Reconciler** | 60s tick (all replicas) | running → idle (if pod doesn't exist in k8s) |
| **API handler** (delete) | `DELETE /tenants/{id}` | any → deleted (removes DynamoDB record, pod, PVC) |

---

## High Availability Design

The orchestrator runs **2 replicas** behind a Kubernetes Service. Both replicas are active and can serve API requests. Coordination uses three mechanisms:

### 1. Redis Distributed Wake Lock

Prevents two replicas from simultaneously creating a pod for the same tenant.

```
Redis key:    tenant:waking:{tenantID}
Command:      SET key "1" NX EX 240
TTL:          240 seconds (auto-expires on replica crash)

Replica A acquires lock → creates pod, waits ready, updates DynamoDB, releases lock
Replica B fails to acquire → polls DynamoDB every 2s until status=running, returns pod_ip
```

### 2. Kubernetes Lease Leader Election

Only one replica runs the idle timeout loop (to avoid duplicate pod deletions).

```
Lease resource:  orchestrator-leader (namespace: tenants)
Lease duration:  15s
Renew deadline:  10s
Retry period:    2s

Leader: runs checkIdleTenants() every 30s
Follower: standby, takes over within ~15s if leader fails
```

### 3. DynamoDB Conditional Writes

Tenant creation uses `attribute_not_exists(tenant_id)` condition to prevent duplicates.

### Reconciler (All Replicas)

The reconciler runs on **every** replica (not leader-elected) because it is read-heavy and idempotent. If multiple replicas detect the same stale tenant, the DynamoDB update is harmless (same state transition).

### Router HA

The router also runs 2 replicas. Both are stateless — they share the same Redis cache and call the same Orchestrator Service endpoint. No coordination needed.

---

## Karpenter Node Provisioning

### Why Bare Metal

Kata Containers requires hardware virtualization (`/dev/kvm`). Standard EC2 instances run inside a hypervisor and do not expose nested virtualization. Only `.metal` instances provide direct hardware access.

### NodePool: `kata-metal`

```yaml
requirements:
  - key: karpenter.k8s.aws/instance-category
    operator: In
    values: ["c", "m", "r"]          # compute, general, memory families
  - key: karpenter.k8s.aws/instance-size
    operator: In
    values: ["metal"]                 # bare metal only
  - key: karpenter.k8s.aws/instance-generation
    operator: Gt
    values: ["5"]                     # gen 6+
  - key: karpenter.sh/capacity-type
    operator: In
    values: ["on-demand"]
  - key: kubernetes.io/arch
    operator: In
    values: ["amd64"]

taints:
  - key: kata-runtime
    value: "true"
    effect: NoSchedule                # only kata-tolerating pods schedule here

disruption:
  consolidationPolicy: WhenEmpty
  consolidateAfter: 60s
```

### EC2NodeClass: `kata`

- **AMI**: AL2023 (latest)
- **Disk**: 500Gi gp3, encrypted
- **userData**: Bootstraps devmapper thin-pool for containerd, required by kata-qemu

### Devmapper Setup (userData)

Kata-qemu with virtiofs requires the devmapper snapshotter in containerd. The userData script:

1. Creates loop-backed thin-pool files (`/var/lib/containerd/.../data` 100G, `meta` 40G)
2. Sets up device-mapper `devpool` target
3. Disables `discard_unpacked_layers` in containerd config (required for multi-snapshotter)
4. Adds `[plugins."io.containerd.snapshotter.v1.devmapper"]` config section
5. Creates a systemd service for reboot persistence

---

## Runtime Selection: kata-qemu

| Runtime | S3 CSI (virtiofs) | Boot Time | Memory Overhead |
|---------|-------------------|-----------|-----------------|
| **kata-qemu** (chosen) | ✅ Works | ~1-2s | ~50-100MB/VM |
| kata-fc (Firecracker) | ❌ No virtiofs | ~100-200ms | ~20MB/VM |
| runc (standard) | ✅ Works | ~50ms | Minimal |

**Why kata-qemu**: Firecracker deliberately omits virtiofs support. Without virtiofs, the host S3 CSI FUSE mount cannot be shared into the VM — it falls back to an empty tmpfs silently. This is an architectural limitation, not a configuration issue. QEMU supports virtiofs natively.

---

## Security

### BotToken Storage

- **Stored in**: DynamoDB `tenant-registry` table, `bot_token` field
- **Redacted from**: All public API responses (`GET /tenants`, `GET /tenants/:id`, `POST /tenants` response)
- **Accessible via**: `GET /tenants/:id/bot_token` — internal endpoint used by Router to send Telegram messages
- **Passed to pod**: Set as `TELEGRAM_BOT_TOKEN` env var on pod creation (used by ZeroClaw entrypoint for webhook reply signing)

### Tenant Isolation

- **VM-level**: Each tenant pod runs in a dedicated Kata VM (QEMU), providing hardware-enforced isolation
- **Storage**: Each tenant has its own S3 prefix (`tenants/{tenantID}/`) and dedicated PV/PVC
- **IAM**: All tenant pods share `zeroclaw-tenant` service account (Bedrock-only permissions). S3 access is via the S3 CSI driver (node-level), not pod-level IAM
- **Network**: Pod-to-pod network is open by default. Consider adding Cilium/Calico NetworkPolicy for cross-tenant restriction.

### Shared IAM Trade-off

All tenant pods use a single IAM role for Bedrock access. This means CloudTrail cannot attribute Bedrock API calls per tenant. Application-level usage tracking is needed for billing and abuse detection.
