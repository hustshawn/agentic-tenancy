# Operations Guide

Day-to-day operations for managing the Agentic Tenancy platform.

---

## ztm CLI Reference

`ztm` is a bash CLI that wraps the Orchestrator and Router APIs. It can call APIs directly via HTTP or through `kubectl exec` (no port-forward needed).

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ZTM_ORCHESTRATOR_URL` | _(empty — uses kubectl exec)_ | Orchestrator HTTP base URL |
| `ZTM_ROUTER_URL` | _(empty — uses kubectl exec)_ | Router public base URL |
| `ZTM_NAMESPACE` | `tenants` | Kubernetes namespace |
| `ZTM_KUBE_CONTEXT` | _(current context)_ | kubectl context override |

### Commands

#### Create Tenant

```bash
ztm tenant create <id> <bot_token> [--idle-timeout <secs>]
```

Creates a DynamoDB record and auto-registers the Telegram webhook (if `ROUTER_PUBLIC_URL` is configured on the orchestrator).

```bash
# Example: create tenant with 1-hour idle timeout
ztm tenant create alice 1234567890:AAHxyz --idle-timeout 3600
```

#### List Tenants

```bash
ztm tenant list
```

Returns all tenants with status, last_active_at, idle_timeout_s (BotToken redacted).

#### Get Tenant

```bash
ztm tenant get <id>
```

```bash
ztm tenant get alice
```

#### Update Tenant

```bash
ztm tenant update <id> [--bot-token <token>] [--idle-timeout <secs>]
```

Updates one or both fields. If bot_token is changed, the Telegram webhook is re-registered automatically.

```bash
# Update bot token
ztm tenant update alice --bot-token 9876543210:AAHnew

# Update idle timeout to 30 minutes
ztm tenant update alice --idle-timeout 1800
```

#### Delete Tenant

```bash
ztm tenant delete <id>
```

Deletes the DynamoDB record, pod (if running), PVC/PV, Redis cache entry, and Telegram webhook.

```bash
ztm tenant delete alice
```

#### Register Webhook

```bash
ztm webhook register <id>
```

Manually registers the Telegram webhook for a tenant. Normally not needed if `ROUTER_PUBLIC_URL` is set (auto-registered on create).

```bash
ztm webhook register alice
```

---

## Tenant Lifecycle

### Create → Test → Delete

```bash
# 1. Create tenant
ztm tenant create mybot 1234567890:AAHxyz --idle-timeout 600

# 2. Verify record created
ztm tenant get mybot

# 3. Register webhook (if not auto-registered)
ztm webhook register mybot

# 4. Send a message to @MyBot on Telegram
#    - First message triggers wake: "⏳ Starting up..."
#    - Pod starts, agent processes message, replies

# 5. Check pod is running
kubectl -n tenants get pod zeroclaw-mybot

# 6. After idle timeout, pod auto-terminates
# 7. Next message wakes it again

# 8. Delete when done
ztm tenant delete mybot
```

### Updating Bot Token

When you need to rotate a Telegram bot token:

```bash
ztm tenant update mybot --bot-token <NEW_TOKEN>
```

This updates DynamoDB and re-registers the webhook with the new token. If the tenant pod is running, it will continue using the old token until restarted. To force a restart:

```bash
kubectl -n tenants delete pod zeroclaw-mybot
```

The next message will wake a new pod with the updated token.

---

## Checking Tenant Status

### Via ztm

```bash
ztm tenant get alice
```

Output shows `status` field: `idle`, `running`, `provisioning`, or `terminated`.

### Via kubectl

```bash
# All tenant pods
kubectl -n tenants get pods -l app=zeroclaw

# Specific tenant
kubectl -n tenants get pod zeroclaw-alice

# Warm pool pods
kubectl -n tenants get pods -l app=warm-pool
```

---

## Manual Pod Wake / Kill

### Wake a pod (without Telegram message)

```bash
kubectl -n tenants exec deployment/orchestrator -- \
  wget -qO- --post-data='' http://localhost:8080/wake/alice
```

### Kill a pod (immediate restart on next message)

```bash
kubectl -n tenants delete pod zeroclaw-alice
```

The reconciler will detect the missing pod within 60s and reset DynamoDB status to `idle`. Or you can wait — the router handles stale cache by invalidating and re-waking.

### Force kill (skip graceful shutdown — data loss risk)

```bash
kubectl -n tenants delete pod zeroclaw-alice --grace-period=0 --force
```

⚠️ This skips the SIGTERM handler that copies brain.db to S3. Use only when the pod is stuck.

---

## Redis Cache Operations

### Check cached pod IP

```bash
kubectl -n tenants exec deployment/redis -- redis-cli GET router:endpoint:alice
```

### Clear cache for one tenant

```bash
kubectl -n tenants exec deployment/redis -- redis-cli DEL router:endpoint:alice
```

### Clear all endpoint cache

```bash
kubectl -n tenants exec deployment/redis -- redis-cli --scan --pattern 'router:endpoint:*' | \
  xargs -r kubectl -n tenants exec deployment/redis -- redis-cli DEL
```

### Check wake locks

```bash
kubectl -n tenants exec deployment/redis -- redis-cli --scan --pattern 'tenant:waking:*'
```

### Clear a stuck wake lock

```bash
kubectl -n tenants exec deployment/redis -- redis-cli DEL tenant:waking:alice
```

---

## Viewing Logs

### Orchestrator

```bash
# Recent logs (warm pool, reconciler, wake events, lifecycle)
kubectl -n tenants logs deployment/orchestrator --tail=100

# Follow live
kubectl -n tenants logs deployment/orchestrator -f

# Specific replica
kubectl -n tenants logs orchestrator-<pod-hash> --tail=50
```

Key log messages:
- `warm pool hit: reusing node` — warm pod claimed successfully
- `warm pool miss: cold start` — no warm pods, Karpenter will provision
- `reconciler: pod missing, resetting state` — stale DynamoDB entry cleaned up
- `leader election: became leader` — this replica is running idle timeout
- `idle check: terminating idle tenant` — pod being shut down for inactivity

### Router

```bash
kubectl -n tenants logs deployment/router --tail=100
```

Key log messages:
- `forwarded to pod` — message successfully delivered to ZeroClaw
- `forward to pod failed, invalidating cache` — stale pod IP, cache cleared
- `wake failed` — orchestrator couldn't start the pod
- `webhook registered` — Telegram webhook set successfully

### Tenant Agent (ZeroClaw)

```bash
kubectl -n tenants logs zeroclaw-alice --tail=100
```

---

## Build & Deploy

### build-and-deploy.sh

Builds Docker images, pushes to ECR, and restarts deployments.

```bash
# Build and deploy everything
./scripts/build-and-deploy.sh all

# Build only orchestrator
./scripts/build-and-deploy.sh orchestrator

# Build only router
./scripts/build-and-deploy.sh router

# Build only zeroclaw (takes effect on next pod wake, no rollout needed)
./scripts/build-and-deploy.sh zeroclaw
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ECR_REGISTRY` | `<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com` | ECR registry URL |
| `AWS_REGION` | `<AWS_REGION>` | AWS region |
| `KUBE_CONTEXT` | `arn:aws:eks:<AWS_REGION>:<AWS_ACCOUNT_ID>:cluster/<EKS_CLUSTER_NAME>` | kubectl context |
| `NAMESPACE` | `tenants` | Kubernetes namespace |
| `PLATFORMS` | `linux/amd64,linux/arm64` | Build platforms (orchestrator/router) |
| `ZEROCLAW_DIR` | `./zeroclaw` | ZeroClaw source directory (path to checked-out zeroclaw repo) |

### Notes

- Orchestrator and router are multi-arch (`amd64` + `arm64`)
- ZeroClaw is `amd64`-only (kata-qemu nodes are always amd64 metal)
- ZeroClaw image changes take effect on next pod wake (no rollout — pods are ephemeral)
- Orchestrator/router changes trigger a rolling restart via `kubectl rollout restart`

---

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| Message sent but no reply, no "⏳ Starting up..." | Telegram webhook not registered or wrong URL | `ztm webhook register <id>` — verify with `curl https://api.telegram.org/bot<TOKEN>/getWebhookInfo` |
| "⏳ Starting up..." sent but no reply follows | Wake failed or pod stuck in Pending | Check `kubectl -n tenants logs deployment/orchestrator --tail=50` for errors. Check `kubectl -n tenants get pod zeroclaw-<id>` status. |
| Pod running but messages not forwarded | Stale Redis cache pointing to old pod IP | `kubectl -n tenants exec deployment/redis -- redis-cli DEL router:endpoint:<id>` |
| Duplicate pods created for same tenant | Redis wake lock not working (Redis down or unreachable) | Check Redis connectivity. Verify `REDIS_ADDR` env var on orchestrator. |
| Bot responds but with wrong persona/model | Pod using stale ZeroClaw image or wrong config | Rebuild zeroclaw: `./scripts/build-and-deploy.sh zeroclaw`, then delete the running pod: `kubectl -n tenants delete pod zeroclaw-<id>` |
| Tenant shows `status=running` but pod doesn't exist | Reconciler hasn't run yet (or is failing) | Wait 60s for reconciler, or manually: `kubectl -n tenants exec deployment/orchestrator -- wget -qO- --method=PATCH --header='Content-Type: application/json' --body-data='{}' http://localhost:8080/tenants/<id>` — or just clear Redis and let router re-wake |
| BotToken field empty in API response | Expected — BotToken is always redacted from public endpoints | Use `GET /tenants/:id/bot_token` (internal endpoint) if you need the actual token |
| Warm pool not creating pods | WARM_POOL_TARGET=0 or no kata-metal nodes available | Check `kubectl -n tenants get deployment warm-pool`. Check Karpenter logs for node provisioning failures. |
| Pod takes 3-5 minutes to start | Warm pool exhausted, Karpenter provisioning new metal node | Increase `WARM_POOL_TARGET` to maintain more pre-warmed nodes |
| Node stuck in NotReady | Devmapper setup failed in userData | Check node's cloud-init logs: `kubectl debug node/<name> -it --image=ubuntu -- cat /var/log/cloud-init-output.log` |
| `forward to pod failed` in router logs, then retry works | Pod IP changed (pod restarted between cache set and use) | Self-healing: router invalidates cache on failure, next request re-wakes. No action needed. |
| Multiple orchestrator replicas both trying to create same pod | Wake lock TTL expired before pod was ready | Increase `WakeLockTTL` (currently 240s). Check if pod creation is abnormally slow. |
