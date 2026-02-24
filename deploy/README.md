# Deployment Manifests

This directory contains Kubernetes manifests for deploying the Agentic Tenancy platform to AWS EKS.

## Prerequisites

Before deploying, ensure you have:

1. **EKS Cluster** with Kata Containers runtime (`kata-qemu` RuntimeClass)
2. **Karpenter** installed and configured
3. **S3 CSI Driver** (Mountpoint for Amazon S3) add-on installed
4. **DynamoDB Table** created (default: `tenant-registry`)
5. **S3 Bucket** created (default: `zeroclaw-tenant-state`)
6. **Redis** deployed in-cluster (accessible at `redis.tenants.svc.cluster.local:6379`)
7. **ECR Repositories** created for: `orchestrator`, `router`, `zeroclaw`
8. **Container Images** built and pushed to ECR (see `../scripts/build-and-deploy.sh`)

---

## Manifest Files

| File | Description |
|------|-------------|
| `00-prerequisites.yaml` | Namespace, ServiceAccounts, RBAC, PriorityClasses |
| `01-orchestrator.yaml` | Orchestrator and Router deployments + services |
| `02-karpenter.yaml` | Karpenter NodePool and EC2NodeClass for Kata metal nodes |

---

## Configuration Steps

### 1. Replace Placeholders

The manifests contain placeholders that must be replaced with your actual AWS and cluster values:

#### **01-orchestrator.yaml**

| Placeholder | Replace With | Example |
|-------------|--------------|---------|
| `<AWS_ACCOUNT_ID>` | Your AWS account ID | `123456789012` |
| `<AWS_REGION>` | Your AWS region | `us-west-2` |
| `<YOUR_ROUTER_DOMAIN>` | Your router's public domain | `https://router.example.com` |

**Environment Variables to Configure:**
```yaml
env:
- name: DYNAMODB_TABLE
  value: "tenant-registry"           # Your DynamoDB table name
- name: K8S_NAMESPACE
  value: "tenants"                   # Kubernetes namespace for tenants
- name: S3_BUCKET
  value: "zeroclaw-tenant-state"     # Your S3 bucket name
- name: WARM_POOL_TARGET
  value: "20"                        # Number of warm pods (0 to disable)
- name: ZEROCLAW_IMAGE
  value: "<AWS_ACCOUNT_ID>.dkr.ecr.<AWS_REGION>.amazonaws.com/zeroclaw:latest"
- name: KATA_RUNTIME_CLASS
  value: "kata-qemu"                 # RuntimeClass name
- name: ROUTER_PUBLIC_URL
  value: "https://<YOUR_ROUTER_DOMAIN>"
```

**Redis Configuration:**
```yaml
- name: REDIS_ADDR
  valueFrom:
    secretKeyRef:
      name: orchestrator-config
      key: redis-addr              # Should contain: redis.tenants.svc.cluster.local:6379
```

#### **02-karpenter.yaml**

| Placeholder | Replace With | Example |
|-------------|--------------|---------|
| `<EKS_CLUSTER_NAME>` | Your EKS cluster name | `my-eks-cluster` |

**Occurrences:**
- `EC2NodeClass.spec.role`: IAM role name for Karpenter nodes
- `EC2NodeClass.spec.tags`: Discovery tag for node configuration
- `EC2NodeClass.spec.securityGroupSelectorTerms`: Security group discovery tag
- `EC2NodeClass.spec.subnetSelectorTerms`: Subnet discovery tag

---

### 2. Example: Replace Placeholders

**Option A: Manual Edit**

Edit the files directly with your values:

```bash
# Edit orchestrator deployment
vim deploy/01-orchestrator.yaml

# Replace all occurrences
:%s/<AWS_ACCOUNT_ID>/123456789012/g
:%s/<AWS_REGION>/us-west-2/g
:%s/<YOUR_ROUTER_DOMAIN>/router.example.com/g
:wq

# Edit Karpenter configuration
vim deploy/02-karpenter.yaml
:%s/<EKS_CLUSTER_NAME>/my-eks-cluster/g
:wq
```

**Option B: Using `sed`**

```bash
# Set your values
export AWS_ACCOUNT_ID="123456789012"
export AWS_REGION="us-west-2"
export ROUTER_DOMAIN="router.example.com"
export EKS_CLUSTER_NAME="my-eks-cluster"

# Replace in orchestrator manifest
sed -i "s|<AWS_ACCOUNT_ID>|${AWS_ACCOUNT_ID}|g" deploy/01-orchestrator.yaml
sed -i "s|<AWS_REGION>|${AWS_REGION}|g" deploy/01-orchestrator.yaml
sed -i "s|<YOUR_ROUTER_DOMAIN>|${ROUTER_DOMAIN}|g" deploy/01-orchestrator.yaml

# Replace in Karpenter manifest
sed -i "s|<EKS_CLUSTER_NAME>|${EKS_CLUSTER_NAME}|g" deploy/02-karpenter.yaml
```

**Option C: Using `envsubst` (Recommended for CI/CD)**

```bash
# Export environment variables
export AWS_ACCOUNT_ID="123456789012"
export AWS_REGION="us-west-2"
export YOUR_ROUTER_DOMAIN="router.example.com"
export EKS_CLUSTER_NAME="my-eks-cluster"

# Generate configured manifests
envsubst < deploy/01-orchestrator.yaml > /tmp/01-orchestrator-configured.yaml
envsubst < deploy/02-karpenter.yaml > /tmp/02-karpenter-configured.yaml

# Apply configured manifests
kubectl apply -f /tmp/01-orchestrator-configured.yaml
kubectl apply -f /tmp/02-karpenter-configured.yaml
```

---

### 3. Create Redis Secret

The orchestrator requires a secret containing the Redis address:

```bash
kubectl create namespace tenants
kubectl create secret generic orchestrator-config \
  --namespace=tenants \
  --from-literal=redis-addr='redis.tenants.svc.cluster.local:6379'
```

---

### 4. Deploy in Order

Apply manifests in the following sequence:

```bash
# 1. Prerequisites (namespace, RBAC, service accounts)
kubectl apply -f deploy/00-prerequisites.yaml

# 2. Create Redis secret
kubectl create secret generic orchestrator-config \
  --namespace=tenants \
  --from-literal=redis-addr='redis.tenants.svc.cluster.local:6379'

# 3. Deploy Karpenter resources (NodePool + EC2NodeClass)
kubectl apply -f deploy/02-karpenter.yaml

# 4. Deploy orchestrator + router
kubectl apply -f deploy/01-orchestrator.yaml
```

---

### 5. Verify Deployment

```bash
# Check all pods are running
kubectl get pods -n tenants

# Expected output:
# NAME                            READY   STATUS    RESTARTS   AGE
# orchestrator-xxxxxxxxxx-xxxxx   1/1     Running   0          2m
# orchestrator-xxxxxxxxxx-xxxxx   1/1     Running   0          2m
# router-xxxxxxxxxx-xxxxx         1/1     Running   0          2m
# router-xxxxxxxxxx-xxxxx         1/1     Running   0          2m
# warm-pool-xxxxxxxxxx-xxxxx      1/1     Running   0          2m
# (... up to WARM_POOL_TARGET replicas)

# Check orchestrator logs
kubectl logs -n tenants deployment/orchestrator --tail=20

# Check router logs
kubectl logs -n tenants deployment/router --tail=20

# Verify warm pool is running (if enabled)
kubectl get deployment warm-pool -n tenants
```

---

## Configuration Reference

### Tuning Warm Pool

The warm pool pre-starts ZeroClaw pods for fast tenant wake times (~13-20s vs 3-5min cold start).

**Adjust replica count:**
```yaml
- name: WARM_POOL_TARGET
  value: "20"    # Number of warm pods (set to 0 to disable)
```

**To disable warm pool entirely:**
```bash
kubectl set env deployment/orchestrator WARM_POOL_TARGET=0 -n tenants
kubectl delete deployment warm-pool -n tenants
```

**To re-enable:**
```bash
kubectl set env deployment/orchestrator WARM_POOL_TARGET=10 -n tenants
kubectl rollout restart deployment/orchestrator -n tenants
```

### Adjusting Resources

Modify resource requests/limits in `01-orchestrator.yaml`:

```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi
```

### Scaling Components

```bash
# Scale orchestrator replicas (HA mode)
kubectl scale deployment orchestrator --replicas=3 -n tenants

# Scale router replicas (load distribution)
kubectl scale deployment router --replicas=5 -n tenants
```

---

## Troubleshooting

### Pods stuck in `ImagePullBackOff`

- **Cause**: ECR image not found or incorrect placeholder replacement
- **Fix**:
  1. Verify images exist in ECR: `aws ecr list-images --repository-name orchestrator`
  2. Check placeholder replacement in manifests
  3. Ensure nodes have ECR pull permissions (via IAM role)

### Warm pool not starting

- **Cause**: `WARM_POOL_TARGET=0` or orchestrator not running
- **Fix**:
  ```bash
  kubectl get deployment orchestrator -n tenants -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="WARM_POOL_TARGET")].value}'
  # If output is "0", set to desired value:
  kubectl set env deployment/orchestrator WARM_POOL_TARGET=10 -n tenants
  ```

### Redis connection errors

- **Cause**: Secret not created or Redis not deployed
- **Fix**:
  ```bash
  # Check secret exists
  kubectl get secret orchestrator-config -n tenants

  # Verify Redis is accessible
  kubectl run -it --rm redis-test --image=redis:alpine --restart=Never -n tenants -- \
    redis-cli -h redis.tenants.svc.cluster.local PING
  ```

### Karpenter not provisioning nodes

- **Cause**: Incorrect `<EKS_CLUSTER_NAME>` tag replacement
- **Fix**:
  1. Verify discovery tags: `kubectl get ec2nodeclass kata-metal -o yaml`
  2. Check AWS resources have matching tags:
     - Security groups: `karpenter.sh/discovery: <EKS_CLUSTER_NAME>`
     - Subnets: `karpenter.sh/discovery: <EKS_CLUSTER_NAME>`

---

## Next Steps

After successful deployment:

1. **Create your first tenant**:
   ```bash
   ztm tenant create alice "<BOT_TOKEN>" --idle-timeout 3600
   ```

2. **Register Telegram webhook**:
   ```bash
   ztm webhook register alice
   ```

3. **Send a test message** to your bot on Telegram

See [../docs/operations.md](../docs/operations.md) for complete operational guides.
